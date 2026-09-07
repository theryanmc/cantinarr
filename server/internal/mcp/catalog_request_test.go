package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/bookdiscovery"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type toolBookCatalog struct{ bookdiscovery.Catalog }

func (toolBookCatalog) Search(context.Context, string, int) ([]byte, error) {
	return []byte(`{"page":1,"results":[{"foreign_id":"ol:OL1W","title":"Public book","authors":["Author"]}]}`), nil
}
func (toolBookCatalog) Book(context.Context, string) ([]byte, error) {
	return []byte(`{"foreign_id":"ol:OL1W","title":"Public book","authors":["Author"]}`), nil
}
func (toolBookCatalog) ResolveClient(context.Context, *chaptarr.Client, string) (bookdiscovery.Targets, error) {
	return bookdiscovery.Targets{State: "needs_match", Suggestions: []bookdiscovery.Target{{ForeignID: "native:choice", Title: "Public book", Author: "Author"}}}, nil
}

func TestPublicCatalogToolsRetainIdentityAndExplicitMatchChoice(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	res, err := database.Exec(`INSERT INTO users(username,password_hash,role) VALUES('reader','','user')`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{3}, 32))
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{ServiceType: "chaptarr", Name: "Selected books", URL: upstream.URL, APIKey: "fixture"}
	if err = store.Create(inst); err != nil {
		t.Fatal(err)
	}
	if err = store.SetUserDefault(uid, "chaptarr", inst.ID); err != nil {
		t.Fatal(err)
	}
	registry := instance.NewRegistry(store)
	service := request.NewService(database, registry, nil, nil)
	service.BookCatalog = toolBookCatalog{}
	server := NewToolServer(nil, service, registry, nil)
	search, err := server.searchBooks(json.RawMessage(`{"query":"Public book","catalog":"all"}`), uid)
	if err != nil || !strings.Contains(search.Text, `"provider":"openlibrary"`) || !strings.Contains(search.Text, `"source":"library"`) || !strings.Contains(search.Text, `"error"`) {
		t.Fatalf("independent catalogs lost: %+v %v", search, err)
	}
	display, err := server.displayMedia(context.Background(), json.RawMessage(`{"items":[{"media_type":"book","title":"Public book","catalog_ref":{"provider":"openlibrary","id":"OL1W"}}]}`), uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	cards, _ := json.Marshal(display.StructuredData)
	if !strings.Contains(string(cards), `"catalog_ref":{"provider":"openlibrary","id":"OL1W"}`) {
		t.Fatalf("card lost source identity: %s", cards)
	}
	out, err := service.CreateMediaRequest(uid, &request.CreateRequest{MediaType: "book", Title: "Public book", BookFormat: "ebook", CatalogRef: &request.CatalogRef{Provider: "openlibrary", ID: "OL1W"}})
	if err != nil {
		t.Fatal(err)
	}
	service.SweepDispatch(context.Background())
	status, err := server.checkRequestStatus(json.RawMessage(`{"media_type":"book","catalog_ref":{"provider":"openlibrary","id":"OL1W"}}`), uid)
	if err != nil || !strings.Contains(status.Text, `"matches"`) || !strings.Contains(status.Text, `native:choice`) || !strings.Contains(status.Text, `Never choose a suggestion automatically`) {
		t.Fatalf("choice not exposed: %+v %v", status, err)
	}
	if _, err = service.DeliveryAction(context.Background(), uid, out.RequestID, "confirm", "invented"); err == nil {
		t.Fatal("invented model identity accepted")
	}
	if _, err = database.Exec(`DELETE FROM user_default_instances WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	display, err = server.displayMedia(context.Background(), json.RawMessage(`{"items":[{"media_type":"book","title":"Public book","catalog_ref":{"provider":"openlibrary","id":"OL1W"}}]}`), uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	cards, _ = json.Marshal(display.StructuredData)
	if strings.Contains(string(cards), `OL1W`) {
		t.Fatal("revoked catalog leaked a card")
	}
}
