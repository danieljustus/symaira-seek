package db

import (
	"math"
	"sort"
)

func Float32SliceToBytes(slice []float32) []byte {
	buf := make([]byte, len(slice)*4)
	for i, f := range slice {
		bits := math.Float32bits(f)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func BytesToFloat32Slice(buf []byte) []float32 {
	if len(buf)%4 != 0 {
		return nil
	}
	slice := make([]float32, len(buf)/4)
	for i := range slice {
		bits := uint32(buf[i*4]) |
			uint32(buf[i*4+1])<<8 |
			uint32(buf[i*4+2])<<16 |
			uint32(buf[i*4+3])<<24
		slice[i] = math.Float32frombits(bits)
	}
	return slice
}

func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dotProduct / (math.Sqrt(normA) * math.Sqrt(normB)))
}

func CosineSimilarityWithStoredNorm(queryVec []float32, embBytes []byte, queryNorm, storedNorm float32) float32 {
	if len(embBytes)%4 != 0 || len(embBytes)/4 != len(queryVec) || len(queryVec) == 0 {
		return 0
	}
	var dotProduct float64
	for i, q := range queryVec {
		offset := i * 4
		bits := uint32(embBytes[offset]) |
			uint32(embBytes[offset+1])<<8 |
			uint32(embBytes[offset+2])<<16 |
			uint32(embBytes[offset+3])<<24
		dotProduct += float64(q * math.Float32frombits(bits))
	}
	denom := float64(queryNorm) * float64(storedNorm)
	if denom == 0 {
		return 0
	}
	return float32(dotProduct / denom)
}

func l2Norm(v []float32) float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x * x)
	}
	return float32(math.Sqrt(sum))
}

func appendSortedByScoreDesc(list []*SearchResult, res *SearchResult) []*SearchResult {
	pos := sort.Search(len(list), func(i int) bool {
		return list[i].CosineScore < res.CosineScore
	})
	list = append(list, nil)
	copy(list[pos+1:], list[pos:len(list)-1])
	list[pos] = res
	return list
}
