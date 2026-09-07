package request

import (
	"context"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/bookdiscovery"
)

func (s *Service) authorizeCatalogMetadata(userID int64, mediaType, instanceID string) error {
	if mediaType != "book" && mediaType != "music" {
		return fmt.Errorf("invalid catalog media type")
	}
	if s.userIsAdmin(userID) && instanceID == "" {
		return nil
	}
	_, err := s.deliveryInstance(userID, mediaType, instanceID)
	return err
}

// CatalogMetadata is the same grant-checked metadata path used by discovery.
// Source references do not grant permission to inspect an arr or request it.
func (s *Service) CatalogMetadata(ctx context.Context, userID int64, mediaType, instanceID string, ref *CatalogRef) ([]byte, error) {
	if ref == nil {
		return nil, fmt.Errorf("catalog reference is required")
	}
	if err := validateCatalogRef(mediaType, ref); err != nil {
		return nil, err
	}
	if err := s.authorizeCatalogMetadata(userID, mediaType, instanceID); err != nil {
		return nil, err
	}
	var body []byte
	var err error
	if mediaType == "book" {
		body, err = s.BookCatalog.Book(ctx, ref.ID)
	} else {
		body, err = s.MusicCatalog.Album(ctx, ref.ID)
	}
	if err != nil {
		return nil, err
	}
	if err = s.authorizeCatalogMetadata(userID, mediaType, instanceID); err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Service) CatalogMatches(ctx context.Context, userID int64, instanceID string, ref *CatalogRef) (bookdiscovery.Targets, error) {
	if ref == nil || validateCatalogRef("book", ref) != nil {
		return bookdiscovery.Targets{}, fmt.Errorf("an Open Library reference is required")
	}
	id, err := s.deliveryInstance(userID, "book", instanceID)
	if err != nil {
		return bookdiscovery.Targets{}, err
	}
	client, _, _ := s.resolveChaptarr(userID, id)
	matches, err := s.BookCatalog.ResolveClient(ctx, client, ref.ID)
	if err != nil {
		return matches, err
	}
	_, err = s.deliveryInstance(userID, "book", id)
	return matches, err
}

func (s *Service) SearchCatalog(userID int64, mediaType, query, instanceID string, page int) ([]byte, error) {
	authorize := func() error {
		if s.userIsAdmin(userID) && instanceID == "" {
			return nil
		}
		_, err := s.deliveryInstance(userID, mediaType, instanceID)
		return err
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	var body []byte
	var err error
	switch mediaType {
	case "book":
		body, err = s.BookCatalog.Search(context.Background(), query, page)
	case "music":
		body, err = s.MusicCatalog.Search(context.Background(), query, page)
	default:
		return nil, fmt.Errorf("unsupported catalog")
	}
	if accessErr := authorize(); accessErr != nil {
		return nil, accessErr
	}
	return body, err
}
