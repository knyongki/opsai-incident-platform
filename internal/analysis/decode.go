package analysis

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Decode accepts exactly one version-1 result and never returns input in errors.
func Decode(data []byte, incidentID string) (Result, error) {
	var result Result
	if len(data) > MaxJSONBytes || !utf8.Valid(data) || !validEscapes(data) {
		return result, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !checkValue(decoder, 0) {
		return result, ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, ErrInvalid
	}
	fields, ok := objectFields(data, "summary", "primary_suspected_cause", "contributing_factors", "observed_conditions", "recommended_actions", "safety_notes", "confidence", "evidence")
	if !ok || !checkObjects(fields["recommended_actions"], "order", "description", "reason") || !checkObjects(fields["evidence"], "source_id", "claim") {
		return result, ErrInvalid
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, ErrInvalid
	}
	if err := Validate(result, incidentID); err != nil {
		return Result{}, ErrInvalid
	}
	return result, nil
}

func objectFields(data []byte, keys ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != len(keys) {
		return nil, false
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return nil, false
		}
	}
	return fields, true
}

func checkObjects(data []byte, keys ...string) bool {
	var items []json.RawMessage
	if json.Unmarshal(data, &items) != nil {
		return false
	}
	for _, item := range items {
		if _, ok := objectFields(item, keys...); !ok {
			return false
		}
	}
	return true
}

// Token inspection catches duplicate decoded keys before unmarshalling can discard them.
func checkValue(decoder *json.Decoder, depth int) bool {
	token, err := decoder.Token()
	if err != nil || token == nil {
		return false
	}
	switch value := token.(type) {
	case string:
		return !strings.ContainsRune(value, 0)
	case json.Delim:
		if depth >= 16 {
			return false
		}
		switch value {
		case '{':
			seen := make(map[string]bool)
			for decoder.More() {
				keyToken, err := decoder.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok || seen[key] || strings.ContainsRune(key, 0) {
					return false
				}
				seen[key] = true
				if !checkValue(decoder, depth+1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !checkValue(decoder, depth+1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	return true
}

// encoding/json replaces unpaired UTF-16 surrogates; reject them instead of losing data.
func validEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
