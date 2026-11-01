package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/dkrest1/hookgarde/apps/api/internal/model"
)

const MaxBody = 256 * 1024

func decode(w http.ResponseWriter, r *http.Request, dst any) ([]byte, error) {
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return nil, model.Fail(415, "unsupported_media_type", "Content-Type must be application/json")
	}
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if e != nil {
		var max *http.MaxBytesError
		if errors.As(e, &max) {
			return nil, model.Fail(413, "body_too_large", "Request exceeds 256 KiB")
		}
		return nil, model.Fail(400, "invalid_json", "Cannot read request body")
	}
	if !utf8.Valid(body) || len(bytes.TrimSpace(body)) == 0 || bytes.TrimSpace(body)[0] != '{' {
		return nil, model.Fail(400, "invalid_json", "Expected a JSON object")
	}
	check := json.NewDecoder(bytes.NewReader(body))
	check.UseNumber()
	if e = uniqueValue(check, 0); e != nil {
		return nil, model.Fail(400, "invalid_json", "Malformed JSON or duplicate object property")
	}
	if _, e = check.Token(); e != io.EOF {
		return nil, model.Fail(400, "invalid_json", "Trailing JSON is not allowed")
	}
	var properties map[string]json.RawMessage
	if e = json.Unmarshal(body, &properties); e != nil {
		return nil, model.Fail(400, "invalid_json", "Malformed JSON")
	}
	allowed := map[string]bool{}
	typ := reflect.TypeOf(dst).Elem()
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" {
			allowed[name] = true
		}
	}
	for name, value := range properties {
		if !allowed[name] || (bytes.Equal(bytes.TrimSpace(value), []byte("null")) && name != "expires_at") {
			return nil, model.Fail(400, "invalid_json", "Unknown property or unexpected null")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(dst); e != nil {
		return nil, model.Fail(400, "invalid_json", "Invalid or unknown JSON property")
	}
	return body, nil
}
func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("excessive nesting")
	}
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return errors.New("duplicate property")
			}
			seen[key] = true
			if e = uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
		t, e = d.Token()
		if e != nil || t != json.Delim('}') {
			return errors.New("object incomplete")
		}
	case '[':
		for d.More() {
			if e = uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
		t, e = d.Token()
		if e != nil || t != json.Delim(']') {
			return errors.New("array incomplete")
		}
	default:
		return errors.New("unexpected delimiter")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != 204 {
		_ = json.NewEncoder(w).Encode(v)
	}
}
