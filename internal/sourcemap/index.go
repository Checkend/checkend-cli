// Package sourcemap builds the compact source map index of Checkend (format v1), so that
// `ce sourcemaps upload` can send a prebuilt index and the server does no work. The format and
// the checks follow src/core/sourcemap-index.ts of the server; the tests compare the bytes with
// indexes that the server builds.
//
// Index format v1, little-endian; u32 = unsigned 32-bit, i32 = signed 32-bit:
//
//	header      9 x u32: magic "CKSI", version, lines, segments, sources, names,
//	            source name bytes, name bytes, content bytes
//	lineStarts  u32[lines + 1]      the first segment of each generated line
//	segments    i32[segments * 5]   generated column, source (-1: none), original line,
//	                                original column, name (-1: none); all 0-based
//	sourceEnds  u32[sources + 1]    offsets into the source name bytes
//	nameEnds    u32[names + 1]      offsets into the name bytes
//	contentEnds u32[sources + 1]    offsets into the content bytes (empty: no content)
//	then the UTF-8 bytes: source names, names, contents.
package sourcemap

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"
)

// ContentType is the content type of an index upload.
const ContentType = "application/x-checkend-sourcemap-index"

const (
	magic       = 0x4953_4b43 // "CKSI" read as a little-endian u32
	version     = 1
	fields      = 5
	maxSegments = 2_000_000
	maxLines    = 1_000_000
	maxValue    = 0x7fff_ffff
)

// InvalidError is a source map that the server would refuse. The messages are the server's.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(msg string) error { return &InvalidError{msg} }

const base64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var digit = func() (d [128]int8) {
	for i := range d {
		d[i] = -1
	}
	for i := 0; i < len(base64); i++ {
		d[base64[i]] = int8(i)
	}
	return d
}()

// decodeMappings decodes the mappings into absolute values, per generated line: each segment is
// [column] or [column, source, line, column] or [column, source, line, column, name].
func decodeMappings(mappings string) ([][][]int64, error) {
	if mappings == "" {
		return nil, nil
	}
	var lines [][][]int64
	var segments [][]int64
	var segment []int64
	var state [fields]int64
	var value, count int64
	shift := 0
	endSegment := func() error {
		if len(segment) == 0 {
			return nil
		}
		if n := len(segment); n != 1 && n != 4 && n != 5 {
			return invalid("A mapping segment has 2 or 3 fields.")
		}
		segments = append(segments, segment)
		count++
		if count > maxSegments {
			return invalid("The source map has too many mappings.")
		}
		segment = nil
		return nil
	}
	for i := 0; i <= len(mappings); i++ {
		code := byte(';') // the end is like ";"
		if i < len(mappings) {
			code = mappings[i]
		}
		if code == ',' || code == ';' {
			if shift != 0 {
				return nil, invalid("A VLQ value is not complete.")
			}
			if err := endSegment(); err != nil {
				return nil, err
			}
			if code == ';' {
				lines = append(lines, segments)
				if len(lines) > maxLines {
					return nil, invalid("The source map has too many lines.")
				}
				segments = nil
				state[0] = 0
			}
			continue
		}
		d := int8(-1)
		if code < 128 {
			d = digit[code]
		}
		if d < 0 {
			return nil, invalid("The mappings have a character that is not base64.")
		}
		value += int64(d&31) << shift
		shift += 5
		if shift > 35 {
			return nil, invalid("A VLQ value is too large.")
		}
		if d&32 != 0 {
			continue
		}
		delta := value / 2
		if value%2 == 1 {
			delta = -delta
		}
		field := len(segment)
		if field >= fields {
			return nil, invalid("A mapping segment has more than 5 fields.")
		}
		state[field] += delta
		if state[field] < 0 {
			return nil, invalid("A mapping value is negative.")
		}
		segment = append(segment, state[field])
		value, shift = 0, 0
	}
	return lines, nil
}

type sourceMap struct {
	sources  []string
	names    []string
	mappings string
	contents []string
}

// stringList reads a JSON list of strings; with nullable, null items give ok=true and "".
func stringList(raw json.RawMessage, nullable bool) ([]string, bool) {
	var items []*string
	if json.Unmarshal(raw, &items) != nil || items == nil {
		return nil, false
	}
	var check []any
	_ = json.Unmarshal(raw, &check)
	out := make([]string, len(items))
	for i, item := range items {
		if item == nil {
			if !nullable || check[i] != nil {
				return nil, false
			}
			continue
		}
		out[i] = *item
	}
	return out, true
}

func checkMap(data []byte) (*sourceMap, error) {
	var top any
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, invalid("The source map is not valid JSON.")
	}
	m := map[string]json.RawMessage{}
	switch top.(type) {
	case map[string]any:
		_ = json.Unmarshal(data, &m)
	case []any:
		// A JavaScript array is an object without these keys.
	default:
		return nil, invalid("Not a JSON object.")
	}
	if _, ok := m["sections"]; ok {
		return nil, invalid("Index maps (sections) are not supported.")
	}
	var v any
	if json.Unmarshal(m["version"], &v) != nil || v != float64(3) {
		return nil, invalid("Only source map version 3 is supported.")
	}
	var sm sourceMap
	if json.Unmarshal(m["mappings"], &sm.mappings) != nil || !isString(m["mappings"]) {
		return nil, invalid("mappings is not a string.")
	}
	sources, ok := stringList(m["sources"], true)
	if !ok {
		return nil, invalid("sources is not a list of strings.")
	}
	if raw, ok := m["names"]; ok {
		if sm.names, ok = stringList(raw, false); !ok {
			return nil, invalid("names is not a list of strings.")
		}
	}
	var contents []string
	if raw, ok := m["sourcesContent"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		if contents, ok = stringList(raw, true); !ok {
			return nil, invalid("sourcesContent is not a list of strings.")
		}
	}
	var root string
	if isString(m["sourceRoot"]) {
		_ = json.Unmarshal(m["sourceRoot"], &root)
		if root != "" && !strings.HasSuffix(root, "/") {
			root += "/"
		}
	}
	var rawSources []any
	_ = json.Unmarshal(m["sources"], &rawSources)
	sm.sources = make([]string, len(sources))
	sm.contents = make([]string, len(sources))
	for i, s := range sources {
		if rawSources[i] != nil {
			sm.sources[i] = root + s
		}
		if i < len(contents) {
			sm.contents[i] = contents[i]
		}
	}
	return &sm, nil
}

func isString(raw json.RawMessage) bool {
	var s string
	return len(raw) > 0 && json.Unmarshal(raw, &s) == nil
}

type table struct {
	ends  []uint32
	bytes []byte
}

func encodeAll(values []string) table {
	t := table{ends: make([]uint32, 1, len(values)+1)}
	for _, v := range values {
		t.bytes = append(t.bytes, v...)
		t.ends = append(t.ends, uint32(len(t.bytes)))
	}
	return t
}

// Build makes the index of a source map (the JSON text). The error is an *InvalidError.
func Build(data []byte) ([]byte, error) {
	sm, err := checkMap(data)
	if err != nil {
		return nil, err
	}
	lines, err := decodeMappings(sm.mappings)
	if err != nil {
		return nil, err
	}
	segmentCount := 0
	for _, line := range lines {
		segmentCount += len(line)
	}
	sources, names, contents := encodeAll(sm.sources), encodeAll(sm.names), encodeAll(sm.contents)

	size := 4*(9+len(lines)+1+segmentCount*fields+2*(len(sm.sources)+1)+len(sm.names)+1) +
		len(sources.bytes) + len(names.bytes) + len(contents.bytes)
	out := make([]byte, 0, size)
	u32 := func(v uint32) { out = binary.LittleEndian.AppendUint32(out, v) }
	for _, v := range []int{magic, version, len(lines), segmentCount, len(sm.sources), len(sm.names),
		len(sources.bytes), len(names.bytes), len(contents.bytes)} {
		u32(uint32(v))
	}
	start := 0
	for _, line := range lines {
		u32(uint32(start))
		start += len(line)
	}
	u32(uint32(start))
	for _, line := range lines {
		sorted := append([][]int64(nil), line...)
		sort.SliceStable(sorted, func(a, b int) bool { return sorted[a][0] < sorted[b][0] })
		for _, s := range sorted {
			source, name := int64(-1), int64(-1)
			values := []int64{s[0], -1, 0, 0, -1}
			if len(s) >= 4 {
				source = s[1]
				values[1], values[2], values[3] = s[1], s[2], s[3]
			}
			if len(s) == 5 {
				name = s[4]
				values[4] = name
			}
			if source >= int64(len(sm.sources)) {
				return nil, invalid("A mapping names a source that is not listed.")
			}
			if name >= int64(len(sm.names)) {
				return nil, invalid("A mapping names a name that is not listed.")
			}
			for _, v := range values {
				if v > maxValue {
					return nil, invalid("A mapping value is too large.")
				}
			}
			for _, v := range values {
				u32(uint32(int32(v)))
			}
		}
	}
	for _, t := range []table{sources, names, contents} {
		for _, end := range t.ends {
			u32(end)
		}
	}
	for _, t := range []table{sources, names, contents} {
		out = append(out, t.bytes...)
	}
	return out, nil
}
