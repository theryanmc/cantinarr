package request

import (
	"context"
	"fmt"
)

func (s *Service) approveDelivery(adminID, id int64, override *DecisionOverride) (*CreateResponse, error) {
	if !s.userIsAdmin(adminID) {
		return nil, fmt.Errorf("only admins can approve requests")
	}
	r, _, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	if r.parkReason == bookParkReasonAuthorImport {
		return nil, fmt.Errorf("the request completes automatically once the import lands")
	}
	if override != nil && override.BookFormat != "" && normalizeBookFormat(override.BookFormat) != r.bookFormat {
		return nil, fmt.Errorf("approve the saved formats; additional formats require a separate request")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE request_log SET approved_by=?,decided_at=CURRENT_TIMESTAMP,park_reason='delivery' WHERE id=? AND status='pending' AND park_reason IS NULL`, adminID, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("this request is not waiting for approval")
	}
	if _, err = tx.Exec(`UPDATE request_dispatch SET state='queued',next_attempt_at=0,message='' WHERE request_id=? AND state='approval'`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if s.notifier != nil {
		audience := []bookRequestSubscriber{{UserID: r.userID, BookFormat: r.bookFormat}}
		if r.mediaType == "book" {
			if subscribers, e := s.bookRequestAudience(id, r.userID, r.bookFormat); e == nil {
				audience = subscribers
			}
		}
		for _, subscriber := range audience {
			s.notifier.NotifyUser(subscriber.UserID, "request_decision", map[string]interface{}{"request_id": id, "tmdb_id": 0, "media_type": r.mediaType, "foreign_id": r.foreignID, "title": r.title, "instance_id": r.instanceID, "decision": "approved", "book_format": subscriber.BookFormat})
		}
	}
	s.dispatchRequest(context.Background(), id)
	return s.deliveryResponse(r.userID, []int64{id}, r.title, r.instanceID, nil)
}

// DeliveryStatus is the common app/MCP read, including unresolved source IDs.
// It authorizes before touching saved intent and again before returning it.
func (s *Service) DeliveryAction(ctx context.Context, userID, id int64, action, foreignID string) (*CreateResponse, error) {
	r, status, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	admin := s.userIsAdmin(userID)
	if action == "cancel" {
		return s.cancelDelivery(userID, id, r, admin)
	}
	if !admin && r.userID != userID {
		return nil, fmt.Errorf("only the requester or an admin can change this request")
	}
	if status != StatusPending {
		return nil, fmt.Errorf("this request is no longer waiting for delivery")
	}
	if action != "cancel" {
		if _, err = s.deliveryInstance(userID, r.mediaType, r.instanceID); err != nil {
			return nil, err
		}
	}
	if !s.hasDispatch(id) {
		return nil, fmt.Errorf("this request has no saved delivery")
	}
	var provider, sourceID string
	if err = s.db.QueryRow(`SELECT COALESCE(catalog_provider,''),COALESCE(catalog_id,'') FROM request_log WHERE id=?`, id).Scan(&provider, &sourceID); err != nil {
		return nil, err
	}
	if action == "confirm" {
		if provider != "openlibrary" || foreignID == "" {
			return nil, fmt.Errorf("choose a matching book")
		}
		client, _, _ := s.resolveChaptarr(userID, r.instanceID)
		matches, e := s.BookCatalog.ResolveClient(ctx, client, sourceID)
		if e != nil {
			return nil, e
		}
		found := false
		for _, candidate := range append(matches.Candidates, matches.Suggestions...) {
			found = found || candidate.ForeignID == foreignID
		}
		if !found {
			return nil, fmt.Errorf("that book is not among the current matches; refresh the choices")
		}
	}
	if action != "confirm" && action != "retry" {
		return nil, fmt.Errorf("unsupported delivery action")
	}
	// Matching can wait on external providers; a grant may change during it.
	if _, err = s.deliveryInstance(userID, r.mediaType, r.instanceID); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var processing int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM request_dispatch WHERE request_id=? AND state='processing'`, id).Scan(&processing); err != nil {
		return nil, err
	}
	if processing > 0 {
		return nil, fmt.Errorf("delivery is in progress; refresh before changing this request")
	}
	if action == "confirm" {
		_, err = tx.Exec(`UPDATE request_log SET foreign_id=?,match_confirmed=1 WHERE id=? AND status='pending'`, foreignID, id)
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(`UPDATE request_dispatch SET state=CASE WHEN (SELECT park_reason FROM request_log WHERE id=?) IS NULL THEN 'approval' ELSE 'queued' END,attempts=0,next_attempt_at=0,message='',code=CASE WHEN code IN ('import_failed','import_cancelled','manual_import_retry') THEN 'manual_import_retry' ELSE '' END WHERE request_id=? AND state IN ('attention','needs_match','retry') AND EXISTS(SELECT 1 FROM request_log WHERE id=? AND status='pending')`, id, id, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.notifyDelivery(id, "queued")
	var ref *CatalogRef
	if provider != "" {
		ref = &CatalogRef{Provider: provider, ID: sourceID}
	}
	return s.deliveryResponse(r.userID, []int64{id}, r.title, r.instanceID, ref)
}
