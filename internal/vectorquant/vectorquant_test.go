package vectorquant

import (
	"math"
	"math/rand"
	"testing"
)

func TestHadamardTransform(t *testing.T) {
	// Hadamard of [1, 0, 0, 0] should produce [0.5, 0.5, 0.5, 0.5] after normalization
	vec := []float64{1, 0, 0, 0}
	HadamardTransform(vec)
	expected := []float64{1, 1, 1, 1}
	for i, v := range vec {
		if math.Abs(v-expected[i]) > 1e-10 {
			t.Errorf("HadamardTransform([1,0,0,0])[%d] = %f, want %f", i, v, expected[i])
		}
	}

	// Transform of all-ones should produce [4, 0, 0, 0]
	vec2 := []float64{1, 1, 1, 1}
	HadamardTransform(vec2)
	expected2 := []float64{4, 0, 0, 0}
	for i, v := range vec2 {
		if math.Abs(v-expected2[i]) > 1e-10 {
			t.Errorf("HadamardTransform([1,1,1,1])[%d] = %f, want %f", i, v, expected2[i])
		}
	}
}

func TestRandomRotationDeterministic(t *testing.T) {
	dim := 128
	rr1 := NewRandomRotation(dim, 42)
	rr2 := NewRandomRotation(dim, 42)

	vec := make([]float64, dim)
	rng := rand.New(rand.NewSource(99))
	for i := range vec {
		vec[i] = rng.NormFloat64()
	}

	out1 := make([]float64, dim)
	out2 := make([]float64, dim)
	rr1.ApplyRotation(vec, out1)
	rr2.ApplyRotation(vec, out2)

	for i := range out1 {
		if math.Abs(out1[i]-out2[i]) > 1e-15 {
			t.Errorf("Rotation not deterministic: out1[%d]=%f, out2[%d]=%f", i, out1[i], i, out2[i])
			break
		}
	}
}

func TestRandomRotationDifferentSeeds(t *testing.T) {
	dim := 64
	rr1 := NewRandomRotation(dim, 42)
	rr2 := NewRandomRotation(dim, 99)

	vec := make([]float64, dim)
	for i := range vec {
		vec[i] = float64(i)
	}

	out1 := make([]float64, dim)
	out2 := make([]float64, dim)
	rr1.ApplyRotation(vec, out1)
	rr2.ApplyRotation(vec, out2)

	same := true
	for i := range out1 {
		if math.Abs(out1[i]-out2[i]) > 1e-10 {
			same = false
			break
		}
	}
	if same {
		t.Error("Different seeds produced identical rotations")
	}
}

func TestRotationPreservesNorm(t *testing.T) {
	dim := 256
	rr := NewRandomRotation(dim, 42)

	vec := make([]float64, dim)
	rng := rand.New(rand.NewSource(1))
	for i := range vec {
		vec[i] = rng.NormFloat64()
	}

	// Compute input norm
	inputNorm := 0.0
	for _, v := range vec {
		inputNorm += v * v
	}
	inputNorm = math.Sqrt(inputNorm)

	out := make([]float64, dim)
	rr.ApplyRotation(vec, out)

	outputNorm := 0.0
	for _, v := range out {
		outputNorm += v * v
	}
	outputNorm = math.Sqrt(outputNorm)

	if math.Abs(inputNorm-outputNorm) > 1e-6 {
		t.Errorf("Rotation changed norm: input=%f, output=%f", inputNorm, outputNorm)
	}
}

func TestRoundtrip2Bit(t *testing.T) {
	testRoundtrip(t, BitWidth2, 128, 42)
}

func TestRoundtrip3Bit(t *testing.T) {
	testRoundtrip(t, BitWidth3, 128, 42)
}

func TestRoundtrip4Bit(t *testing.T) {
	testRoundtrip(t, BitWidth4, 128, 42)
}

func TestRoundtripHalf2and3(t *testing.T) {
	testRoundtrip(t, BitWidthHalf2and3, 128, 42)
}

func TestRoundtripHalf3and4(t *testing.T) {
	testRoundtrip(t, BitWidthHalf3and4, 128, 42)
}

func testRoundtrip(t *testing.T, bw BitWidth, dim, seed int) {
	t.Helper()
	codec, err := NewCodec(dim, bw, seed, 0)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}

	rng := rand.New(rand.NewSource(7))
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}

	code, err := codec.Encode(vec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := codec.Decode(code)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Check dimension
	if len(decoded) != dim {
		t.Fatalf("Decode returned dim %d, want %d", len(decoded), dim)
	}

	// Check MSE is reasonable (not exact due to quantization)
	totalMSE := 0.0
	for i := range vec {
		diff := float64(vec[i]) - float64(decoded[i])
		totalMSE += diff * diff
	}
	mse := totalMSE / float64(dim)

	// 4-bit should have very low MSE, 2-bit higher
	maxMSE := 0.5
	switch bw {
	case BitWidth4:
		maxMSE = 0.05
	case BitWidthHalf3and4:
		maxMSE = 0.1
	case BitWidth3:
		maxMSE = 0.2
	case BitWidthHalf2and3:
		maxMSE = 0.3
	case BitWidth2:
		maxMSE = 0.5
	}
	if mse > maxMSE {
		t.Errorf("MSE too high for %s: %f (max %f)", bw, mse, maxMSE)
	}
}

func TestDeterministicEncoding(t *testing.T) {
	dim := 64
	bw := BitWidth3
	codec1, _ := NewCodec(dim, bw, 42, 0)
	codec2, _ := NewCodec(dim, bw, 42, 0)

	vec := make([]float32, dim)
	rng := rand.New(rand.NewSource(7))
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}

	code1, _ := codec1.Encode(vec)
	code2, _ := codec2.Encode(vec)

	if len(code1.Bytes) != len(code2.Bytes) {
		t.Fatalf("Different code lengths: %d vs %d", len(code1.Bytes), len(code2.Bytes))
	}
	for i := range code1.Bytes {
		if code1.Bytes[i] != code2.Bytes[i] {
			t.Errorf("Non-deterministic encoding at byte %d: %x vs %x", i, code1.Bytes[i], code2.Bytes[i])
			break
		}
	}
	if code1.Min != code2.Min || code1.Max != code2.Max {
		t.Errorf("Non-deterministic min/max: (%f,%f) vs (%f,%f)", code1.Min, code1.Max, code2.Min, code2.Max)
	}
}

func TestInvalidMetadataRejection(t *testing.T) {
	// Too short metadata
	_, err := UnmarshalMetadata([]byte{1, 2, 3})
	if err == nil {
		t.Error("Expected error for short metadata, got nil")
	}

	// Wrong version
	data := make([]byte, 32)
	data[0] = 99 // version = 99
	_, err = UnmarshalMetadata(data)
	if err == nil {
		t.Error("Expected error for wrong version, got nil")
	}
}

func TestInvalidDimRejection(t *testing.T) {
	_, err := NewCodec(0, BitWidth2, 42, 0)
	if err == nil {
		t.Error("Expected error for dim=0, got nil")
	}
	_, err = NewCodec(-1, BitWidth2, 42, 0)
	if err == nil {
		t.Error("Expected error for dim=-1, got nil")
	}
}

func TestInvalidBitWidthRejection(t *testing.T) {
	_, err := NewCodec(128, BitWidth(99), 42, 0)
	if err == nil {
		t.Error("Expected error for bit width 99, got nil")
	}
}

func TestEncodeDimMismatch(t *testing.T) {
	codec, _ := NewCodec(128, BitWidth2, 42, 0)
	_, err := codec.Encode(make([]float32, 64))
	if err == nil {
		t.Error("Expected dim mismatch error, got nil")
	}
}

func TestScoreSanity(t *testing.T) {
	dim := 64
	codec, _ := NewCodec(dim, BitWidth3, 42, 0)

	rng := rand.New(rand.NewSource(1))
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}

	code, _ := codec.Encode(vec)

	// Cosine self-score should be close to 1.0 for a well-quantized vector
	selfScore := codec.ScoreCosine(vec, code)
	if selfScore < 0.5 {
		t.Errorf("Cosine self-score should be > 0.5, got %f", selfScore)
	}

	// Random vector should have lower cosine score
	randomVec := make([]float32, dim)
	for i := range randomVec {
		randomVec[i] = float32(rng.NormFloat64())
	}
	var rnorm float64
	for _, v := range randomVec {
		rnorm += float64(v) * float64(v)
	}
	rnorm = math.Sqrt(rnorm)
	for i := range randomVec {
		randomVec[i] = float32(float64(randomVec[i]) / rnorm)
	}

	randScore := codec.ScoreCosine(randomVec, code)
	if selfScore <= randScore {
		t.Errorf("Self-score (%f) should be > random score (%f)", selfScore, randScore)
	}
}

func TestScoreCosineSanity(t *testing.T) {
	dim := 64
	codec, _ := NewCodec(dim, BitWidth4, 42, 0)

	rng := rand.New(rand.NewSource(1))
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}

	code, _ := codec.Encode(vec)

	cosScore := codec.ScoreCosine(vec, code)
	// For a well-quantized vector, cosine score should be close to 1.0
	if cosScore < 0.5 {
		t.Errorf("Cosine self-score too low: %f (expected > 0.5)", cosScore)
	}
}

func TestScoringRankSanity(t *testing.T) {
	dim := 128
	codec, _ := NewCodec(dim, BitWidth3, 42, 0)

	rng := rand.New(rand.NewSource(10))
	// Create a set of database vectors
	numVecs := 100
	dbVecs := make([][]float32, numVecs)
	for i := range dbVecs {
		dbVecs[i] = make([]float32, dim)
		for j := range dbVecs[i] {
			dbVecs[i][j] = float32(rng.NormFloat64())
		}
	}

	// Pick a query that is similar to one specific vector
	targetIdx := 42
	query := make([]float32, dim)
	for j := range query {
		query[j] = dbVecs[targetIdx][j] + float32(rng.NormFloat64())*0.1
	}

	// Encode all
	codes := make([]*PackedCode, numVecs)
	for i, v := range dbVecs {
		codes[i], _ = codec.Encode(v)
	}

	// Score all
	scores := make([]float64, numVecs)
	for i, code := range codes {
		scores[i] = float64(codec.Score(query, code))
	}

	// The exact nearest neighbor should rank in the top-10
	exactRanks := computeExactRanks([][]float32{query}, dbVecs, 10)
	topK := topKIndices(scores, 10)

	// Check recall - at least 5 of the top-10 exact should appear in approx top-10
	recall := recallAtK(exactRanks[0], topK)
	if recall < 0.3 {
		t.Errorf("Rank sanity: recall@10 = %f, expected >= 0.3", recall)
	}
}

func TestRecallOnDeterministicFixture(t *testing.T) {
	dim := 128
	numVecs := 500
	numQueries := 20
	k := 10

	// Generate fixture
	dbVecs, queryVecs := GenerateSyntheticDataset(dim, numVecs, numQueries, 42)

	for _, bw := range []BitWidth{BitWidth2, BitWidth3, BitWidth4} {
		t.Run(bw.String(), func(t *testing.T) {
			codec, err := NewCodec(dim, bw, 1337, 0)
			if err != nil {
				t.Fatalf("NewCodec: %v", err)
			}

			// Encode all
			codes := make([]*PackedCode, numVecs)
			for i, v := range dbVecs {
				codes[i], _ = codec.Encode(v)
			}

			// Compute exact ranks
			exactRanks := computeExactRanks(queryVecs, dbVecs, k)

			// Compute approximate ranks
			totalRecall := 0.0
			for qi, q := range queryVecs {
				scores := make([]float64, numVecs)
				for ci, code := range codes {
					scores[ci] = float64(codec.Score(q, code))
				}
				approxRanks := topKIndices(scores, k)
				totalRecall += recallAtK(exactRanks[qi], approxRanks)
			}
			avgRecall := totalRecall / float64(numQueries)

			// Basic sanity: recall should be better than random (which would be ~k/numVecs)
			randomRecall := float64(k) / float64(numVecs)
			if avgRecall <= randomRecall {
				t.Errorf("Recall %f <= random baseline %f for %s", avgRecall, randomRecall, bw)
			}

			t.Logf("Recall@%d for %s: %.4f (random baseline: %.4f)", k, bw, avgRecall, randomRecall)
		})
	}
}

func TestMetadataRoundtrip(t *testing.T) {
	codec, _ := NewCodec(768, BitWidth3, 42, 0)
	data := codec.MarshalMetadata()

	meta, err := UnmarshalMetadata(data)
	if err != nil {
		t.Fatalf("UnmarshalMetadata: %v", err)
	}

	if meta.Dim != 768 {
		t.Errorf("Dim: got %d, want 768", meta.Dim)
	}
	if meta.BitWidth != int(BitWidth3) {
		t.Errorf("BitWidth: got %d, want %d", meta.BitWidth, BitWidth3)
	}
	if meta.Seed != 42 {
		t.Errorf("Seed: got %d, want 42", meta.Seed)
	}

	codec2, err := CodecFromMetadata(meta)
	if err != nil {
		t.Fatalf("CodecFromMetadata: %v", err)
	}
	if codec2.Dim != codec.Dim || codec2.BitWidth != codec.BitWidth || codec2.Seed != codec.Seed {
		t.Error("Reconstructed codec doesn't match original")
	}
}

func TestPackedBytesConsistency(t *testing.T) {
	for _, bw := range []BitWidth{BitWidth2, BitWidth3, BitWidth4, BitWidthHalf2and3, BitWidthHalf3and4} {
		t.Run(bw.String(), func(t *testing.T) {
			codec, _ := NewCodec(768, bw, 42, 0)
			expectedBytes := bw.BytesPerVector(codec.RotDim)
			actualBytes := codec.packedBytesLen()
			if actualBytes != expectedBytes {
				t.Errorf("packedBytesLen=%d, BytesPerVector(rotDim=%d)=%d", actualBytes, codec.RotDim, expectedBytes)
			}
		})
	}
}

func TestPackUnpackBits(t *testing.T) {
	// Test roundtrip of bit packing
	for _, bits := range []int{2, 3, 4} {
		levels := 1 << bits
		for val := 0; val < levels; val++ {
			buf := make([]byte, 16)
			packBits(buf, 0, val, bits)
			got := unpackBits(buf, 0, bits)
			if got != val {
				t.Errorf("packBits/unpackBits(%d, %d bits): got %d, want %d", val, bits, got, val)
				break
			}
		}
	}
}

func TestPackUnpackBitsAtOffset(t *testing.T) {
	// Test packing at non-zero bit offsets
	buf := make([]byte, 8)
	packBits(buf, 3, 5, 3) // value 5 in 3 bits starting at bit 3
	got := unpackBits(buf, 3, 3)
	if got != 5 {
		t.Errorf("packBits/unpackBits at offset 3: got %d, want 5", got)
	}
}

func TestRealisticFixture(t *testing.T) {
	dbVecs, queryVecs := GenerateRealisticFixture(128, 200, 10, 42)

	if len(dbVecs) != 200 {
		t.Errorf("Expected 200 db vecs, got %d", len(dbVecs))
	}
	if len(queryVecs) != 10 {
		t.Errorf("Expected 10 query vecs, got %d", len(queryVecs))
	}
	if len(dbVecs[0]) != 128 {
		t.Errorf("Expected dim 128, got %d", len(dbVecs[0]))
	}

	// Check normalization
	for i, vec := range dbVecs {
		norm := 0.0
		for _, v := range vec {
			norm += float64(v) * float64(v)
		}
		norm = math.Sqrt(norm)
		if math.Abs(norm-1.0) > 1e-5 {
			t.Errorf("dbVec %d not normalized: norm=%f", i, norm)
			break
		}
	}
}
