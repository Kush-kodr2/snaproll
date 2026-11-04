package match

import "math"

// Dot returns the dot product. For unit-length embeddings this is the cosine similarity.
func Dot(a, b []float32) float32 {
	var s float32
	for i := 0; i < min(len(a), len(b)); i++ {
		s += a[i] * b[i]
	}
	return s
}

// MeanUnit averages the vectors and scales the result back to length 1.
func MeanUnit(vecs [][]float32) []float32 {
	out := make([]float32, len(vecs[0]))
	for _, v := range vecs {
		for i := range v {
			out[i] += v[i]
		}
	}
	var norm float64
	for _, x := range out {
		norm += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(norm))
	if n == 0 {
		return out
	}
	for i := range out {
		out[i] /= n
	}
	return out
}