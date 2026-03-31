package mysql

import (
	"fmt"

	gomysql "github.com/go-mysql-org/go-mysql/mysql"
)

// stripQueryAttributes strips the query attribute framing from a COM_QUERY payload
// (after the command byte 0x03 has already been removed) per the MySQL 8.0.31+
// CLIENT_QUERY_ATTRIBUTES protocol extension.
//
// Frame layout:
//
//	parameter_count    length-encoded int  (number of query attributes)
//	parameter_set_count length-encoded int (always 1)
//	-- if parameter_count > 0: --
//	null_bitmap        ceil(parameter_count/8) bytes
//	new_params_bind_flag 1 byte
//	-- if new_params_bind_flag == 1, for each param: --
//	  param_type       2 bytes (little-endian uint16)
//	  param_name       length-encoded string
//	-- for each non-NULL param: --
//	  param_value      length-encoded string
//	-- remaining bytes are the SQL query string --
func stripQueryAttributes(queryBytes []byte) ([]byte, error) {
	cursor := 0

	// Read parameter_count
	paramCount, isNull, n := gomysql.LengthEncodedInt(queryBytes[cursor:])
	if n == 0 {
		return nil, fmt.Errorf("truncated payload reading parameter_count")
	}
	if isNull {
		return nil, fmt.Errorf("unexpected NULL for parameter_count")
	}
	cursor += n

	// Read parameter_set_count (always 1, but we must consume it)
	_, _, n = gomysql.LengthEncodedInt(queryBytes[cursor:])
	if n == 0 {
		return nil, fmt.Errorf("truncated payload reading parameter_set_count")
	}
	cursor += n

	if paramCount == 0 {
		return queryBytes[cursor:], nil
	}

	// NUL-bitmap: ceil(paramCount / 8) bytes
	bitmapLen := int((paramCount + 7) / 8)
	if cursor+bitmapLen > len(queryBytes) {
		return nil, fmt.Errorf("truncated payload reading null bitmap")
	}
	nullBitmap := queryBytes[cursor : cursor+bitmapLen]
	cursor += bitmapLen

	// new_params_bind_flag
	if cursor >= len(queryBytes) {
		return nil, fmt.Errorf("truncated payload reading new_params_bind_flag")
	}
	bindFlag := queryBytes[cursor]
	cursor++

	// If bind flag is set, consume type (2 bytes) + name (lenenc string) per param
	if bindFlag == 1 {
		for i := uint64(0); i < paramCount; i++ {
			// param type: 2 bytes
			if cursor+2 > len(queryBytes) {
				return nil, fmt.Errorf("truncated payload reading param type for param %d", i)
			}
			cursor += 2

			// param name: length-encoded string
			n, err := gomysql.SkipLengthEncodedString(queryBytes[cursor:])
			if err != nil {
				return nil, fmt.Errorf("truncated payload reading param name for param %d: %w", i, err)
			}
			cursor += n
		}
	}

	// Consume param values for non-NULL parameters
	for i := uint64(0); i < paramCount; i++ {
		// Check NUL-bitmap: byte index = i/8, bit index = i%8
		byteIdx := i / 8
		bitIdx := i % 8
		if nullBitmap[byteIdx]&(1<<bitIdx) != 0 {
			// NULL value — no bytes on the wire
			continue
		}

		n, err := gomysql.SkipLengthEncodedString(queryBytes[cursor:])
		if err != nil {
			return nil, fmt.Errorf("truncated payload reading param value for param %d: %w", i, err)
		}
		cursor += n
	}

	return queryBytes[cursor:], nil
}
