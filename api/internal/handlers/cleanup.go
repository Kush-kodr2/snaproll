package handlers

import (
	"context"
	"log"
	"time"
)

// StartCleanup deletes review thumbnails older than 24 hours, even if nobody closed the session.
func (h *Handler) StartCleanup(ctx context.Context) {
	run := func() {
		tag, err := h.DB.Exec(ctx, `DELETE FROM review_faces WHERE created_at < now() - interval '24 hours'`)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("cleanup: %v", err)
			}
			return
		}
		if n := tag.RowsAffected(); n > 0 {
			log.Printf("cleanup: deleted %d old review thumbnails", n)
		}
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}