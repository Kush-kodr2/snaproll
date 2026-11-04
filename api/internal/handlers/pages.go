package handlers

import "net/http"

// servePage serves one HTML file with strict security headers.
// The paths are relative to where you start the server (run it from api/).
func servePage(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' blob:; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, path)
	}
}