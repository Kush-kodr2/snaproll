package match

import "sort"

// Template is one student's enrolled face photos (unit-length embeddings).
type Template struct {
	StudentID int64
	Vecs      [][]float32
}

// Score is the best similarity between a face and any of the student's enrolled photos.
func (t Template) Score(face []float32) float32 {
	best := float32(-1)
	for _, v := range t.Vecs {
		if s := Dot(face, v); s > best {
			best = s
		}
	}
	return best
}

// Result is one face matched to one student.
type Result struct {
	Face      int // index into the faces slice
	StudentID int64
	Score     float32
	Margin    float32 // score minus the best OTHER student's score for the same face
}

// Unmatched is a face that no student claimed.
type Unmatched struct {
	Face int
	Best float32 // its highest score against any student, handy for tuning
}

// AssignPhoto matches the faces of ONE photo to students. A face gets at most one student
// and a student at most one face (a person appears once in a photo). Pairs are taken
// greedily from the highest score down; scores below minScore are ignored.
func AssignPhoto(faces [][]float32, templates []Template, minScore float32) ([]Result, []Unmatched) {
	type pair struct {
		face, tmpl int
		score      float32
	}
	var pairs []pair
	top1 := make([]float32, len(faces)) // best score per face
	top1Idx := make([]int, len(faces))
	top2 := make([]float32, len(faces)) // second-best score per face

	for f, face := range faces {
		top1[f], top1Idx[f], top2[f] = -1, -1, -1
		for t := range templates {
			s := templates[t].Score(face)
			if s > top1[f] {
				top2[f] = top1[f]
				top1[f], top1Idx[f] = s, t
			} else if s > top2[f] {
				top2[f] = s
			}
			if s >= minScore {
				pairs = append(pairs, pair{f, t, s})
			}
		}
	}
	sort.Slice(pairs, func(a, b int) bool { return pairs[a].score > pairs[b].score })

	faceUsed := make([]bool, len(faces))
	tmplUsed := make([]bool, len(templates))
	var results []Result
	for _, p := range pairs {
		if faceUsed[p.face] || tmplUsed[p.tmpl] {
			continue
		}
		faceUsed[p.face], tmplUsed[p.tmpl] = true, true

		// The margin compares against the best competitor for this face. If another student
		// scores higher (but was claimed by a stronger face), the margin is negative and the
		// match ends up in "needs review".
		competitor := top1[p.face]
		if top1Idx[p.face] == p.tmpl {
			competitor = top2[p.face]
		}
		if competitor < 0 {
			competitor = 0 // only one student to compare with
		}
		results = append(results, Result{
			Face: p.face, StudentID: templates[p.tmpl].StudentID,
			Score: p.score, Margin: p.score - competitor,
		})
	}

	var unmatched []Unmatched
	for f := range faces {
		if !faceUsed[f] {
			unmatched = append(unmatched, Unmatched{Face: f, Best: top1[f]})
		}
	}
	return results, unmatched
}