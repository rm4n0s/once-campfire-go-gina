package web

import (
	"context"
	"database/sql"
	"errors"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"

	"github.com/rm4n0s/once-campfire-go-gina/internal/database"
	"github.com/rm4n0s/once-campfire-go-gina/internal/storage"
)

// Mirrors the pinned Rust attachment Assignment: omitted, deleted, uploaded, or
// invalid. Profile params.compact drops nil; account and bot params retain it.
func recordAttachment(r *httpx.Request, field string, upload *storage.Staged, compact bool) database.BlobStager {
	if upload != nil {
		return upload
	}
	if !r.Form.Has(field) || compact && nullParam(r, field) {
		return nil
	}
	return attachmentAssignment{invalid: r.Form.Get(field) != ""}
}

type attachmentAssignment struct{ invalid bool }

func (a attachmentAssignment) Insert(context.Context, *sql.Tx) (int64, error) {
	if a.invalid {
		return 0, errors.New("could not find or build blob: expected attachable")
	}
	return 0, nil
}
func (attachmentAssignment) Keep()    {}
func (attachmentAssignment) Discard() {}
