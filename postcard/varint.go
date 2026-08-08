// Package postcard implements the wire format used by the Rust postcard
// crate (https://postcard.jamesmunns.com/wire-format), which the Aidoku
// source SDK uses to exchange structured data across the WASM host/guest
// boundary.
package postcard

// zigzag encodes a signed integer so that small magnitude values (positive
// or negative) map to small unsigned varints.
func zigzag16(n int16) uint16 {
	return uint16(n<<1) ^ uint16(n>>15)
}

func zigzag32(n int32) uint32 {
	return uint32(n<<1) ^ uint32(n>>31)
}

func zigzag64(n int64) uint64 {
	return uint64(n<<1) ^ uint64(n>>63)
}

func unzigzag16(n uint16) int16 {
	return int16(n>>1) ^ -int16(n&1)
}

func unzigzag32(n uint32) int32 {
	return int32(n>>1) ^ -int32(n&1)
}

func unzigzag64(n uint64) int64 {
	return int64(n>>1) ^ -int64(n&1)
}
