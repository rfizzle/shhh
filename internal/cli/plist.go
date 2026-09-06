package cli

// A reader for the XML property lists `defaults export` writes, enough to
// walk to one key in a terminal's preferences.
//
// macOS keeps an application's settings behind cfprefsd, and the file under
// ~/Library/Preferences can lag what the application would read, so the
// doctor asks `defaults export <domain> -` rather than opening the file. The
// answer is XML holding every value type a plist can, and the standard
// library has no reader for it, so this is one: a dict is a map, an array a
// slice, the scalars themselves, and data and dates their text, since
// nothing that walks the result needs either decoded.

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// plistKey is a dict's key as the decoder meets it, distinct from a string
// value so a dict with a value where a key should be is refused rather than
// read as a key.
type plistKey string

// parsePlist reads the root value of a property list.
func parsePlist(r io.Reader) (any, error) {
	d := xml.NewDecoder(r)
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "plist" {
			return nil, fmt.Errorf("not a property list: <%s>", se.Name.Local)
		}
		v, more, err := plistValue(d)
		if err != nil {
			return nil, err
		}
		if !more {
			return nil, fmt.Errorf("an empty property list")
		}
		return v, nil
	}
}

// plistValue reads the next value under the cursor. more is false when the
// enclosing container closed instead, which is how a dict and an array
// find their end.
func plistValue(d *xml.Decoder) (v any, more bool, err error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, false, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil, false, nil
		case xml.StartElement:
			v, err := plistElement(d, t)
			return v, true, err
		}
	}
}

func plistElement(d *xml.Decoder, se xml.StartElement) (any, error) {
	switch se.Name.Local {
	case "dict":
		m := map[string]any{}
		for {
			k, more, err := plistValue(d)
			if err != nil {
				return nil, err
			}
			if !more {
				return m, nil
			}
			name, ok := k.(plistKey)
			if !ok {
				return nil, fmt.Errorf("a dict entry with no key")
			}
			v, more, err := plistValue(d)
			if err != nil {
				return nil, err
			}
			if !more {
				return nil, fmt.Errorf("key %q has no value", name)
			}
			m[string(name)] = v
		}
	case "array":
		a := []any{}
		for {
			v, more, err := plistValue(d)
			if err != nil {
				return nil, err
			}
			if !more {
				return a, nil
			}
			a = append(a, v)
		}
	case "key":
		s, err := plistText(d, se)
		return plistKey(s), err
	case "string", "date":
		return plistText(d, se)
	case "data":
		// Base64 broken across indented lines; the whitespace is layout.
		s, err := plistText(d, se)
		return strings.Join(strings.Fields(s), ""), err
	case "integer":
		s, err := plistText(d, se)
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	case "real":
		s, err := plistText(d, se)
		if err != nil {
			return nil, err
		}
		return strconv.ParseFloat(strings.TrimSpace(s), 64)
	case "true", "false":
		return se.Name.Local == "true", d.Skip()
	}
	return nil, fmt.Errorf("unknown plist element <%s>", se.Name.Local)
}

func plistText(d *xml.Decoder, se xml.StartElement) (string, error) {
	var s string
	err := d.DecodeElement(&s, &se)
	return s, err
}

// plistAt walks dict keys from a value. A missing step is a missing value,
// not an error: a preference that was never set is absent from the export.
func plistAt(v any, path ...string) (any, bool) {
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[key]; !ok {
			return nil, false
		}
	}
	return v, true
}

func plistString(v any, path ...string) string {
	s, _ := plistAt(v, path...)
	str, _ := s.(string)
	return str
}

func plistBool(v any, path ...string) bool {
	b, _ := plistAt(v, path...)
	on, _ := b.(bool)
	return on
}

func plistInt(v any, path ...string) (int64, bool) {
	i, ok := plistAt(v, path...)
	if !ok {
		return 0, false
	}
	n, ok := i.(int64)
	return n, ok
}
