package vectorquant

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// BitWidth controls the number of quantization levels per dimension.
type BitWidth int

const (
	BitWidth2 BitWidth = 2
	BitWidth3 BitWidth = 3
	BitWidth4 BitWidth = 4

	// Channel-split modes: half dims at one bit-width, half at the next.
	BitWidthHalf2and3 BitWidth = 25 // encodes 2.5-bit effective
	BitWidthHalf3and4 BitWidth = 35 // encodes 3.5-bit effective
)

// Levels returns the number of quantization levels for the bit width.
func (b BitWidth) Levels() int {
	switch b {
	case BitWidth2:
		return 4 // 2^2
	case BitWidth3:
		return 8 // 2^3
	case BitWidth4:
		return 16 // 2^4
	case BitWidthHalf2and3:
		return 0 // special: per-channel
	case BitWidthHalf3and4:
		return 0 // special: per-channel
	default:
		return 0
	}
}

// BytesPerVector returns the number of bytes needed to store one vector of
// the given rotation dimension at this bit width. For channel-split modes,
// this is the weighted average.
func (b BitWidth) BytesPerVector(rotDim int) int {
	switch b {
	case BitWidth2:
		return (rotDim*2 + 7) / 8
	case BitWidth3:
		return (rotDim*3 + 7) / 8
	case BitWidth4:
		return (rotDim*4 + 7) / 8
	case BitWidthHalf2and3:
		half := rotDim / 2
		rest := rotDim - half
		return (half*2+7)/8 + (rest*3+7)/8
	case BitWidthHalf3and4:
		half := rotDim / 2
		rest := rotDim - half
		return (half*3+7)/8 + (rest*4+7)/8
	default:
		return 0
	}
}

// Codec holds the metadata needed to encode and decode vectors using
// TurboQuant-style scalar quantization with a random rotation.
type Codec struct {
	Dim         int         // original vector dimension
	BitWidth    BitWidth    // quantization bit width
	Seed        int         // seed for deterministic random rotation
	BlockSize   int         // dimensions per sub-quantizer block (0 = whole vector)
	RotDim      int         // padded dimension after rotation (power of 2)
	rotation    *RandomRotation
	levelsPerCh []int       // per-channel levels (for channel-split modes; nil otherwise)
}

// Metadata is the serializable header stored alongside packed codes.
type Metadata struct {
	Dim       int
	BitWidth  int
	Seed      int
	BlockSize int
	RotDim    int
	Version   int // currently 1
}

const metadataVersion = 1

// Errors
var (
	ErrDimMismatch    = errors.New("vectorquant: dimension mismatch")
	ErrInvalidBitWidth = errors.New("vectorquant: unsupported bit width")
	ErrCodeTooShort   = errors.New("vectorquant: code bytes too short")
	ErrMetaTooShort   = errors.New("vectorquant: metadata bytes too short")
	ErrMetaVersion    = errors.New("vectorquant: unsupported metadata version")
)

// NewCodec creates a new quantization codec. blockSize controls sub-quantizer
// block size (0 or >= dim means no blocking; per-block min/max).
func NewCodec(dim int, bw BitWidth, seed, blockSize int) (*Codec, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("%w: dim=%d", ErrDimMismatch, dim)
	}
	switch bw {
	case BitWidth2, BitWidth3, BitWidth4, BitWidthHalf2and3, BitWidthHalf3and4:
	default:
		return nil, fmt.Errorf("%w: %d", ErrInvalidBitWidth, bw)
	}

	rotDim := nextPowerOf2(dim)
	c := &Codec{
		Dim:       dim,
		BitWidth:  bw,
		Seed:      seed,
		BlockSize: blockSize,
		RotDim:    rotDim,
		rotation:  NewRandomRotation(dim, seed),
	}

	if bw == BitWidthHalf2and3 || bw == BitWidthHalf3and4 {
		c.levelsPerCh = make([]int, rotDim)
		half := rotDim / 2
		for i := 0; i < half; i++ {
			if bw == BitWidthHalf2and3 {
				c.levelsPerCh[i] = 4 // 2-bit
			} else {
				c.levelsPerCh[i] = 8 // 3-bit
			}
		}
		for i := half; i < rotDim; i++ {
			if bw == BitWidthHalf2and3 {
				c.levelsPerCh[i] = 8 // 3-bit
			} else {
				c.levelsPerCh[i] = 16 // 4-bit
			}
		}
	}

	return c, nil
}

// PackedCode holds the quantized code for a single vector.
type PackedCode struct {
	Bytes []byte // packed quantized indices
	Min   float64 // per-block min (for global block: single value)
	Max   float64 // per-block max (for global block: single value)
}

// quantizeIndex maps a float64 value (in [0, 1] after normalization) to
// a quantization index for the given number of levels.
func quantizeIndex(val float64, levels int) int {
	idx := int(val * float64(levels))
	if idx < 0 {
		idx = 0
	}
	if idx >= levels {
		idx = levels - 1
	}
	return idx
}

// dequantizeMidpoint returns the midpoint of the quantization bin for the
// given index and level count, normalized to [0, 1].
func dequantizeMidpoint(idx, levels int) float64 {
	return (float64(idx) + 0.5) / float64(levels)
}

// Encode quantizes a float32 vector and returns packed bytes + metadata.
// The rotation is applied first, then per-block scalar quantization.
func (c *Codec) Encode(vec []float32) (*PackedCode, error) {
	if len(vec) != c.Dim {
		return nil, fmt.Errorf("%w: got %d, expected %d", ErrDimMismatch, len(vec), c.Dim)
	}

	// Step 1: Apply random rotation
	rotated := make([]float64, c.RotDim)
	floats := make([]float64, c.Dim)
	for i, v := range vec {
		floats[i] = float64(v)
	}
	c.rotation.ApplyRotation(floats, rotated)

	// Step 2: Find global min/max for quantization scale
	minVal, maxVal := math.MaxFloat64, -math.MaxFloat64
	for _, v := range rotated {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	// Add small epsilon to avoid division by zero
	rangeVal := maxVal - minVal
	if rangeVal < 1e-10 {
		rangeVal = 1e-10
	}

	// Step 3: Quantize each dimension
	code := make([]byte, c.packedBytesLen())
	bitOffset := 0

	for i := 0; i < c.RotDim; i++ {
		// Normalize to [0, 1]
		normalized := (rotated[i] - minVal) / rangeVal
		if normalized < 0 {
			normalized = 0
		}
		if normalized > 1 {
			normalized = 1
		}

		levels := c.levelsForChannel(i)
		idx := quantizeIndex(normalized, levels)

		// Pack bits
		packBits(code, bitOffset, idx, c.bitsForChannel(i))
		bitOffset += c.bitsForChannel(i)
	}

	return &PackedCode{
		Bytes: code,
		Min:   minVal,
		Max:   maxVal,
	}, nil
}

// Decode reconstructs an approximate float32 vector from a packed code.
func (c *Codec) Decode(pc *PackedCode) ([]float32, error) {
	if len(pc.Bytes) < c.packedBytesLen() {
		return nil, fmt.Errorf("%w: got %d, need %d", ErrCodeTooShort, len(pc.Bytes), c.packedBytesLen())
	}

	rotated := make([]float64, c.RotDim)
	bitOffset := 0

	for i := 0; i < c.RotDim; i++ {
		levels := c.levelsForChannel(i)
		idx := unpackBits(pc.Bytes, bitOffset, c.bitsForChannel(i))
		midpoint := dequantizeMidpoint(idx, levels)
		rotated[i] = pc.Min + (pc.Max-pc.Min)*midpoint
		bitOffset += c.bitsForChannel(i)
	}

	// Apply inverse rotation
	result := make([]float64, c.RotDim)
	c.rotation.InverseApplyRotation(rotated, result)

	out := make([]float32, c.Dim)
	for i := 0; i < c.Dim; i++ {
		out[i] = float32(result[i])
	}
	return out, nil
}

// Score estimates the inner product between query and the quantized vector
// without fully materializing the dequantized vector. This is the key
// operation for approximate search: score = dot(query_rotated, codebook)
// scaled by the quantization range.
//
// The query is rotated into the same space as the codes, then we compute:
//   Σ_i query_rotated[i] * (min + (max-min) * midpoint_i)
//   = min * Σ query_rotated[i] + (max-min) * Σ query_rotated[i] * midpoint_i
func (c *Codec) Score(query []float32, pc *PackedCode) float32 {
	if len(query) != c.Dim {
		return 0
	}

	// Rotate query
	rotated := make([]float64, c.RotDim)
	floats := make([]float64, c.Dim)
	for i, v := range query {
		floats[i] = float64(v)
	}
	c.rotation.ApplyRotation(floats, rotated)

	// Compute inner product with the quantized codebook
	bitOffset := 0
	dotSum := 0.0
	sumQ := 0.0
	for i := 0; i < c.RotDim; i++ {
		levels := c.levelsForChannel(i)
		idx := unpackBits(pc.Bytes, bitOffset, c.bitsForChannel(i))
		midpoint := dequantizeMidpoint(idx, levels)
		dotSum += rotated[i] * midpoint
		sumQ += rotated[i]
		bitOffset += c.bitsForChannel(i)
	}

	// Reconstruct: dot(rotated_query, rotated_code) ≈
	//   sumQ * min + (max - min) * dotSum
	score := sumQ*pc.Min + (pc.Max-pc.Min)*dotSum
	return float32(score)
}

// ScoreCosine is like Score but normalizes by the query norm and the
// approximate code norm for cosine similarity estimation.
func (c *Codec) ScoreCosine(query []float32, pc *PackedCode) float32 {
	ip := c.Score(query, pc)
	qNorm := 0.0
	for _, v := range query {
		qNorm += float64(v) * float64(v)
	}
	qNorm = math.Sqrt(qNorm)
	if qNorm < 1e-10 {
		return 0
	}

	// Approximate code norm from quantized values
	codeNorm := 0.0
	bitOffset := 0
	for i := 0; i < c.RotDim; i++ {
		levels := c.levelsForChannel(i)
		idx := unpackBits(pc.Bytes, bitOffset, c.bitsForChannel(i))
		midpoint := dequantizeMidpoint(idx, levels)
		v := pc.Min + (pc.Max-pc.Min)*midpoint
		codeNorm += v * v
		bitOffset += c.bitsForChannel(i)
	}
	codeNorm = math.Sqrt(codeNorm)
	if codeNorm < 1e-10 {
		return 0
	}

	return float32(float64(ip) / (qNorm * codeNorm))
}

// --- helpers ---

func (c *Codec) bitsForChannel(i int) int {
	if c.levelsPerCh != nil {
		lvls := c.levelsPerCh[i]
		return bitsForLevels(lvls)
	}
	return bitsForLevels(c.BitWidth.Levels())
}

func (c *Codec) levelsForChannel(i int) int {
	if c.levelsPerCh != nil {
		return c.levelsPerCh[i]
	}
	return c.BitWidth.Levels()
}

func bitsForLevels(levels int) int {
	switch levels {
	case 4:
		return 2
	case 8:
		return 3
	case 16:
		return 4
	default:
		return 0
	}
}

func (c *Codec) packedBytesLen() int {
	totalBits := 0
	for i := 0; i < c.RotDim; i++ {
		totalBits += c.bitsForChannel(i)
	}
	return (totalBits + 7) / 8
}

// packBits writes `bits` bits of value `val` starting at bitOffset in buf.
func packBits(buf []byte, bitOffset, val, bits int) {
	for b := 0; b < bits; b++ {
		bit := (val >> b) & 1
		byteIdx := (bitOffset + b) / 8
		bitIdx := (bitOffset + b) % 8
		if bit == 1 {
			buf[byteIdx] |= 1 << uint(bitIdx)
		}
	}
}

// unpackBits reads `bits` bits starting at bitOffset from buf.
func unpackBits(buf []byte, bitOffset, bits int) int {
	val := 0
	for b := 0; b < bits; b++ {
		byteIdx := (bitOffset + b) / 8
		bitIdx := (bitOffset + b) % 8
		if buf[byteIdx]&(1<<uint(bitIdx)) != 0 {
			val |= 1 << b
		}
	}
	return val
}

// --- Metadata serialization ---

// MarshalMetadata serializes codec metadata to bytes.
func (c *Codec) MarshalMetadata() []byte {
	meta := Metadata{
		Version:   metadataVersion,
		Dim:       c.Dim,
		BitWidth:  int(c.BitWidth),
		Seed:      c.Seed,
		BlockSize: c.BlockSize,
		RotDim:    c.RotDim,
	}
	buf := make([]byte, 32)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(meta.Version))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(meta.Dim))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(meta.BitWidth))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(meta.Seed))
	binary.LittleEndian.PutUint32(buf[16:20], uint32(meta.BlockSize))
	binary.LittleEndian.PutUint32(buf[20:24], uint32(meta.RotDim))
	// Pad to 32 bytes
	return buf
}

// UnmarshalMetadata deserializes codec metadata from bytes.
func UnmarshalMetadata(data []byte) (*Metadata, error) {
	if len(data) < 24 {
		return nil, fmt.Errorf("%w: need 24, got %d", ErrMetaTooShort, len(data))
	}
	meta := &Metadata{
		Version:   int(binary.LittleEndian.Uint32(data[0:4])),
		Dim:       int(binary.LittleEndian.Uint32(data[4:8])),
		BitWidth:  int(binary.LittleEndian.Uint32(data[8:12])),
		Seed:      int(binary.LittleEndian.Uint32(data[12:16])),
		BlockSize: int(binary.LittleEndian.Uint32(data[16:20])),
		RotDim:    int(binary.LittleEndian.Uint32(data[20:24])),
	}
	if meta.Version != metadataVersion {
		return nil, fmt.Errorf("%w: got version %d, expected %d", ErrMetaVersion, meta.Version, metadataVersion)
	}
	return meta, nil
}

// CodecFromMetadata reconstructs a Codec from deserialized metadata.
func CodecFromMetadata(meta *Metadata) (*Codec, error) {
	return NewCodec(meta.Dim, BitWidth(meta.BitWidth), meta.Seed, meta.BlockSize)
}

// ---------------------------------------------------------------------------
// Sidecar integration — bridges the vectorquant codec with the database
// quantised sidecar columns (db.QuantSidecarMeta).
//
// The sidecar BLOB format prepends 8 bytes (2 × float32 little-endian) for
// the per-vector quantisation min/max, followed by the packed bit indices.
// The JSON metadata mirrors db.QuantSidecarMeta's JSON shape so it can be
// serialised directly into the embedding_quant_meta TEXT column.
// ---------------------------------------------------------------------------

// sidecarHeaderBytes is the number of bytes prepended to the packed code
// in the sidecar BLOB to store Min and Max as float32 little-endian.
const sidecarHeaderBytes = 8

// SidecarMeta is the JSON-serialisable metadata stored alongside a
// quantised sidecar.  Its JSON tags match db.QuantSidecarMeta so the
// two types are wire-compatible; vectorquant does not import db to
// preserve the layering boundary.
type SidecarMeta struct {
	CodecVersion   int     `json:"codec_version"`
	Dimension      int     `json:"dimension"`
	BitWidth       int     `json:"bit_width"`
	QuantizerMode  string  `json:"quantizer_mode"`
	ProjectionSeed int64   `json:"projection_seed,omitempty"`
	Norm           float32 `json:"norm,omitempty"`
}

// CodecVersion is the current codec wire version.  Bump whenever the
// quantisation algorithm or packed layout changes.
const CodecVersion = 1

// EncodeSidecar encodes a float32 vector and returns the sidecar BLOB
// (packed code with min/max header) together with the JSON-serialisable
// SidecarMeta ready for database storage.
//
// The caller passes the L2 norm of the original vector (0 if unknown).
// The returned SidecarMeta is wire-compatible with db.QuantSidecarMeta.
func (c *Codec) EncodeSidecar(vec []float32, norm float32) ([]byte, *SidecarMeta, error) {
	code, err := c.Encode(vec)
	if err != nil {
		return nil, nil, err
	}

	// Pack min/max as float32 LE prefix.
	blob := make([]byte, sidecarHeaderBytes+len(code.Bytes))
	binary.LittleEndian.PutUint32(blob[0:4], math.Float32bits(float32(code.Min)))
	binary.LittleEndian.PutUint32(blob[4:8], math.Float32bits(float32(code.Max)))
	copy(blob[sidecarHeaderBytes:], code.Bytes)

	meta := &SidecarMeta{
		CodecVersion:   CodecVersion,
		Dimension:      c.Dim,
		BitWidth:       int(c.BitWidth),
		QuantizerMode:  "scalar",
		ProjectionSeed: int64(c.Seed),
		Norm:           norm,
	}
	return blob, meta, nil
}

// DecodeSidecar reconstructs an approximate float32 vector from a sidecar
// BLOB and its SidecarMeta.  The meta is validated against the codec's
// expectations before decoding.
func DecodeSidecar(blob []byte, meta *SidecarMeta) ([]float32, error) {
	codec, err := CodecFromSidecarMeta(meta)
	if err != nil {
		return nil, err
	}
	code, err := UnpackSidecarBlob(blob)
	if err != nil {
		return nil, err
	}
	return codec.Decode(code)
}

// CodecFromSidecarMeta reconstructs a Codec from a SidecarMeta (the
// JSON metadata stored in the database).  It validates that the
// metadata is complete and uses a supported codec version.
func CodecFromSidecarMeta(meta *SidecarMeta) (*Codec, error) {
	if meta == nil {
		return nil, fmt.Errorf("%w: nil sidecar metadata", ErrInvalidSidecarMeta)
	}
	if meta.CodecVersion != CodecVersion {
		return nil, fmt.Errorf("%w: codec_version=%d, want %d",
			ErrCodecVersionMismatch, meta.CodecVersion, CodecVersion)
	}
	if meta.Dimension <= 0 {
		return nil, fmt.Errorf("%w: dimension=%d", ErrDimMismatch, meta.Dimension)
	}
	if meta.BitWidth <= 0 {
		return nil, fmt.Errorf("%w: bit_width=%d", ErrInvalidBitWidth, meta.BitWidth)
	}
	return NewCodec(meta.Dimension, BitWidth(meta.BitWidth), int(meta.ProjectionSeed), 0)
}

// UnpackSidecarBlob extracts the min/max header and packed code bytes
// from a sidecar BLOB produced by EncodeSidecar.
func UnpackSidecarBlob(blob []byte) (*PackedCode, error) {
	if len(blob) < sidecarHeaderBytes {
		return nil, fmt.Errorf("%w: blob %d bytes, need >= %d",
			ErrCodeTooShort, len(blob), sidecarHeaderBytes)
	}
	minBits := binary.LittleEndian.Uint32(blob[0:4])
	maxBits := binary.LittleEndian.Uint32(blob[4:8])
	return &PackedCode{
		Bytes: blob[sidecarHeaderBytes:],
		Min:   float64(math.Float32frombits(minBits)),
		Max:   float64(math.Float32frombits(maxBits)),
	}, nil
}

// ValidateSidecarMeta checks that meta is internally consistent and
// matches the given codec.  Returns nil when valid.
func ValidateSidecarMeta(meta *SidecarMeta, c *Codec) error {
	if meta == nil {
		return fmt.Errorf("%w: nil sidecar metadata", ErrInvalidSidecarMeta)
	}
	if meta.CodecVersion != CodecVersion {
		return fmt.Errorf("%w: codec_version=%d, want %d",
			ErrCodecVersionMismatch, meta.CodecVersion, CodecVersion)
	}
	if meta.Dimension != c.Dim {
		return fmt.Errorf("%w: meta dimension=%d, codec dimension=%d",
			ErrDimMismatch, meta.Dimension, c.Dim)
	}
	if BitWidth(meta.BitWidth) != c.BitWidth {
		return fmt.Errorf("%w: meta bit_width=%d, codec bit_width=%d",
			ErrInvalidBitWidth, meta.BitWidth, int(c.BitWidth))
	}
	if int(meta.ProjectionSeed) != c.Seed {
		return fmt.Errorf("%w: meta projection_seed=%d, codec seed=%d",
			ErrSeedMismatch, meta.ProjectionSeed, c.Seed)
	}
	return nil
}

// Errors specific to sidecar integration.
var (
	ErrInvalidSidecarMeta = errors.New("vectorquant: invalid sidecar metadata")
	ErrCodecVersionMismatch = errors.New("vectorquant: sidecar codec version mismatch")
	ErrSeedMismatch       = errors.New("vectorquant: sidecar seed mismatch")
)
