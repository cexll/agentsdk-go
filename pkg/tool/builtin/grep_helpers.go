package toolbuiltin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	maxIntValue = int64(1<<(strconv.IntSize-1) - 1)
	minIntValue = -maxIntValue - 1
)

func intFromParam(value interface{}) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return intFromInt64(v)
	case uint:
		return intFromUint64(uint64(v))
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		return intFromUint64(v)
	case float64:
		if v > float64(maxIntValue) || v < float64(minIntValue) {
			return 0, fmt.Errorf("value %v is out of range", v)
		}
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return intFromInt64(int64(v))
	case float32:
		f64 := float64(v)
		if f64 > float64(maxIntValue) || f64 < float64(minIntValue) {
			return 0, fmt.Errorf("value %v is out of range", v)
		}
		if v != float32(int64(v)) {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return intFromInt64(int64(v))
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, err
		}
		return intFromInt64(i)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, errors.New("empty string")
		}
		i, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, err
		}
		return i, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func intFromInt64(v int64) (int, error) {
	if v > maxIntValue || v < minIntValue {
		return 0, fmt.Errorf("value %d is out of range", v)
	}
	return int(v), nil
}

func intFromUint64(v uint64) (int, error) {
	if v > uint64(maxIntValue) {
		return 0, fmt.Errorf("value %d is out of range", v)
	}
	return int(v), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
