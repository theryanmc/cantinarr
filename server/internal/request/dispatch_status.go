package request

// Delivery is intent, never an availability snapshot. Live state can cover a
// format while its saved job is still waiting (for example an admin imports it
// directly). Failed reads preserve the saved job and explicitly mark library
// truth unknown. Completed jobs do not claim a file still exists.
func (s *Service) overlayDeliveryTruth(userID int64, mediaType, foreignID string, out *CreateResponse) {
	known := false
	out.StatusKnown = &known
	if out.CanonicalForeignID != "" {
		foreignID = out.CanonicalForeignID
	}
	if foreignID == "" {
		return
	}
	completed := map[string]bool{}
	for _, d := range out.Delivery {
		completed[d.Format] = d.State == "complete"
		if d.State == "complete" {
			if d.Format == "" {
				out.Status = StatusRequested
			} else {
				out.BookFormats[d.Format] = StatusRequested
			}
		}
	}
	if mediaType == "book" {
		client, instanceID, err := s.resolveChaptarr(userID, out.InstanceID)
		if err != nil || client == nil {
			return
		}
		projection, err := s.liveBookProjectionCached(client, instanceID)
		if err != nil {
			return
		}
		live, err := projection.formatsFor(foreignID)
		if err != nil {
			return
		}
		known = true
		if out.BookFormats == nil {
			out.BookFormats = map[string]string{}
		}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			status, exists := live[format]
			if !exists {
				var recordID int
				for _, d := range out.Delivery {
					if d.Format == format {
						s.db.QueryRow(`SELECT book_record_id FROM request_dispatch WHERE request_id=? AND format=?`, d.RequestID, d.Format).Scan(&recordID)
						break
					}
				}
				if record, ok := projection.recordByID(recordID); ok {
					status, exists = record.Status, true
					if record.ForeignID != "" {
						out.CanonicalForeignID = record.ForeignID
					}
				}
			}
			if exists && status != StatusUnavailable {
				out.BookFormats[format] = status
				delete(out.BookFormatWaits, format)
			} else if completed[format] {
				out.BookFormats[format] = StatusUnavailable
			}
		}
		out.Status = collapseBookStatuses(out.BookFormats, out.Status)
	} else {
		client, instanceID, err := s.resolveLidarr(userID, out.InstanceID)
		if err != nil || client == nil {
			return
		}
		projection, err := s.liveMusicProjectionCached(client, instanceID)
		if err != nil {
			return
		}
		known = true
		status, exists := projection.Statuses[foreignID]
		if !exists {
			var recordID int
			if len(out.Delivery) > 0 {
				s.db.QueryRow(`SELECT book_record_id FROM request_log WHERE id=?`, out.Delivery[0].RequestID).Scan(&recordID)
			}
			if record, ok := projection.recordByID(recordID); ok {
				status, exists = record.Status, true
				if record.ForeignID != "" {
					out.CanonicalForeignID = record.ForeignID
				}
			}
		}
		if exists && status != StatusUnavailable {
			out.Status = status
		} else if completed[""] {
			out.Status = StatusUnavailable
		}
	}
}
