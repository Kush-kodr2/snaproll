package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

const maxImportRows = 500

var enrollmentNoRe = regexp.MustCompile(`^[A-Z0-9/_.-]{3,50}$`)

type importProblem struct {
	Line  int    `json:"line"`
	Error string `json:"error"`
}

type importRow struct {
	line  int
	no    string
	name  string
	email *string
}

// POST /batches/{id}/students/import  (admin; multipart field "file" = CSV)
// First line must be a header: enrollment_no,name,email   (email optional)
func (h *Handler) ImportStudents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	batchID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid batch id")
		return
	}
	var exists bool
	if err := h.DB.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM batches WHERE id = $1)`, batchID).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "batch not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "send a multipart form with a CSV in the 'file' field (max 1 MB)")
		return
	}
	defer file.Close()

	rd := csv.NewReader(file)
	rd.FieldsPerRecord = -1 // rows may differ in length; we validate each one ourselves
	header, err := rd.Read()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "the CSV is empty or unreadable")
		return
	}
	col := map[string]int{}
	for i, name := range header {
		name = strings.TrimPrefix(name, "\ufeff") // Excel adds an invisible marker to UTF-8 CSVs
		col[strings.ToLower(strings.TrimSpace(name))] = i
	}
	noCol, hasNo := col["enrollment_no"]
	nameCol, hasName := col["name"]
	emailCol, hasEmail := col["email"]
	if !hasNo || !hasName {
		writeError(w, http.StatusUnprocessableEntity,
			"the first line must have the columns enrollment_no and name (email is optional)")
		return
	}

	var rows []importRow
	var problems []importProblem
	seen := map[string]int{} // enrollment number -> line where it first appeared
	line := 1
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			problems = append(problems, importProblem{line, "this row could not be read"})
			continue
		}
		cell := func(i int) string {
			if i >= 0 && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		no := strings.ToUpper(cell(noCol))
		name := cell(nameCol)
		emailVal := ""
		if hasEmail {
			emailVal = strings.ToLower(cell(emailCol))
		}
		if no == "" && name == "" {
			continue // blank line
		}
		if len(rows)+len(problems) >= maxImportRows {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("too many rows (max %d per file)", maxImportRows))
			return
		}

		switch {
		case !enrollmentNoRe.MatchString(no):
			problems = append(problems, importProblem{line,
				"enrollment_no must be 3-50 characters: letters, digits, / _ . -"})
		case name == "" || len(name) > 100:
			problems = append(problems, importProblem{line, "name is required (max 100 characters)"})
		case seen[no] != 0:
			problems = append(problems, importProblem{line,
				fmt.Sprintf("%s already appears on line %d", no, seen[no])})
		default:
			var email *string
			if emailVal != "" {
				if a, err := mail.ParseAddress(emailVal); err != nil || a.Address != emailVal {
					problems = append(problems, importProblem{line, "invalid email"})
					continue
				}
				email = &emailVal
			}
			seen[no] = line
			rows = append(rows, importRow{line, no, name, email})
		}
	}
	if len(problems) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "fix these rows and upload again (nothing was imported)", "problems": problems})
		return
	}
	if len(rows) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no student rows found in the CSV")
		return
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	created, skipped := 0, 0
	for _, row := range rows {
		var id int64
		err := tx.QueryRow(ctx,
			`INSERT INTO students (enrollment_no, name, email, batch_id)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (enrollment_no) DO NOTHING
			 RETURNING id`,
			row.no, row.name, row.email, batchID).Scan(&id)
		if err == nil {
			created++
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			serverError(w, err)
			return
		}
		// Already exists: fine if it's in this batch, an error if it belongs to another one.
		var existingBatch int64
		if err := tx.QueryRow(ctx,
			`SELECT batch_id FROM students WHERE enrollment_no = $1`, row.no).Scan(&existingBatch); err != nil {
			serverError(w, err)
			return
		}
		if existingBatch != batchID {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "nothing was imported",
				"problems": []importProblem{{row.line,
					fmt.Sprintf("%s already belongs to a different batch", row.no)}},
			})
			return
		}
		skipped++
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"created": created, "skipped_existing": skipped})
}