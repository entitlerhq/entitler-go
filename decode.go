package entitler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

const maxWhole = 1<<53 - 1

// decodeError names what failed and where in an answer, never any of the answer's text.
type decodeError struct {
	path string
	what string
}

func (e *decodeError) Error() string {
	if e.path == "" {
		return e.what
	}
	return e.what + " at " + e.path
}

var errNotJSON = errors.New("the answer is not valid JSON")

// parseJSON parses a whole body into maps, slices, strings, bools, json.Number and nil. Its errors
// never quote the body.
func parseJSON(body []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, errNotJSON
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errNotJSON
	}
	return v, nil
}

// reader walks a parsed answer, keeping the first failure; every accessor answers a zero value
// once one has failed.
type reader struct{ err error }

func (r *reader) fail(path, what string) {
	if r.err == nil {
		r.err = &decodeError{path: path, what: what}
	}
}

type node struct {
	r    *reader
	path string
	v    any
}

type object struct {
	r    *reader
	path string
	m    map[string]any
}

func decodeBody[T any](body []byte, decode func(object) T) (T, error) {
	v, err := parseJSON(body)
	if err != nil {
		var zero T
		return zero, err
	}
	r := &reader{}
	out := decode(node{r: r, v: v}.object())
	if r.err != nil {
		var zero T
		return zero, r.err
	}
	return out, nil
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func (n node) object() object {
	m, ok := n.v.(map[string]any)
	if !ok {
		n.r.fail(n.path, "expected an object")
	}
	return object{r: n.r, path: n.path, m: m}
}

// at answers a required member, failing when the member is missing.
func (o object) at(name string) node {
	v, ok := o.m[name]
	if !ok && o.m != nil {
		o.r.fail(join(o.path, name), "missing a required member")
	}
	return node{r: o.r, path: join(o.path, name), v: v}
}

// opt answers an optional member and whether it is present.
func (o object) opt(name string) (node, bool) {
	v, ok := o.m[name]
	return node{r: o.r, path: join(o.path, name), v: v}, ok
}

func (o object) has(name string) bool {
	_, ok := o.m[name]
	return ok
}

func (n node) null() bool { return n.v == nil }

func (n node) str() string {
	s, ok := n.v.(string)
	if !ok {
		n.r.fail(n.path, "expected a string")
	}
	return s
}

func (n node) strOrNull() *string {
	if n.null() {
		return nil
	}
	s := n.str()
	return &s
}

func (n node) boolean() bool {
	b, ok := n.v.(bool)
	if !ok {
		n.r.fail(n.path, "expected a boolean")
	}
	return b
}

func (n node) int() int64 {
	num, ok := n.v.(json.Number)
	if !ok {
		n.r.fail(n.path, "expected a whole number")
		return 0
	}
	i, err := strconv.ParseInt(string(num), 10, 64)
	if err != nil || i > maxWhole || i < -maxWhole {
		n.r.fail(n.path, "expected a whole number within 2^53 − 1")
		return 0
	}
	return i
}

func (n node) intOrNull() *int64 {
	if n.null() {
		return nil
	}
	i := n.int()
	return &i
}

func (n node) instant() time.Time {
	s, ok := n.v.(string)
	if !ok {
		n.r.fail(n.path, "expected an RFC 3339 instant")
		return time.Time{}
	}
	t, err := parseInstant(s)
	if err != nil {
		n.r.fail(n.path, "expected an RFC 3339 instant in the years 0001 to 9999")
	}
	return t
}

// instantOrNull answers the zero time for null.
func (n node) instantOrNull() time.Time {
	if n.null() {
		return time.Time{}
	}
	return n.instant()
}

func (n node) value() Value {
	switch v := n.v.(type) {
	case bool:
		if v {
			return On{}
		}
	case string:
		if v == "unlimited" {
			return Unlimited{}
		}
	case json.Number:
		if i := n.int(); i >= 0 {
			return Amount(i)
		}
	}
	n.r.fail(n.path, "expected true, a whole number of 0 or more, or \"unlimited\"")
	return nil
}

// limit decodes an amount or unlimited.
func (n node) limit() Value {
	if b, ok := n.v.(bool); ok && b {
		n.r.fail(n.path, "expected a whole number of 0 or more, or \"unlimited\"")
		return nil
	}
	return n.value()
}

func (n node) list(each func(node)) {
	items, ok := n.v.([]any)
	if !ok {
		n.r.fail(n.path, "expected an array")
		return
	}
	for i, item := range items {
		each(node{r: n.r, path: fmt.Sprintf("%s[%d]", n.path, i), v: item})
	}
}

func listOf[T any](n node, decode func(node) T) []T {
	out := []T{}
	n.list(func(item node) { out = append(out, decode(item)) })
	return out
}

func objects[T any](n node, decode func(object) T) []T {
	return listOf(n, func(item node) T { return decode(item.object()) })
}

func (n node) strings() []string {
	return listOf(n, node.str)
}

func (n node) stringMap() map[string]string {
	o := n.object()
	out := make(map[string]string, len(o.m))
	for k, v := range o.m {
		s, ok := v.(string)
		if !ok {
			n.r.fail(n.path+"[…]", "expected a string")
		}
		out[k] = s
	}
	return out
}

func objectOrNull[T any](n node, decode func(object) T) *T {
	if n.null() {
		return nil
	}
	v := decode(n.object())
	return &v
}

// parseInstant reads an RFC 3339 instant strictly: years 0001 to 9999, real calendar dates and
// times, and offsets up to ±23:59, never rolling an impossible value over into another instant.
func parseInstant(s string) (time.Time, error) {
	bad := errors.New("not an RFC 3339 instant")
	if len(s) < 20 {
		return time.Time{}, bad
	}
	digits := func(from, to int) (int, bool) {
		n := 0
		for i := from; i < to; i++ {
			c := s[i]
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		return n, true
	}
	year, ok1 := digits(0, 4)
	month, ok2 := digits(5, 7)
	day, ok3 := digits(8, 10)
	hour, ok4 := digits(11, 13)
	minute, ok5 := digits(14, 16)
	second, ok6 := digits(17, 19)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || s[4] != '-' || s[7] != '-' || s[13] != ':' ||
		s[16] != ':' || (s[10] != 'T' && s[10] != 't') {
		return time.Time{}, bad
	}
	rest := s[19:]
	nanos := 0
	if rest[0] == '.' {
		i := 1
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 1 {
			return time.Time{}, bad
		}
		frac := rest[1:i]
		for j := 0; j < 9; j++ {
			nanos *= 10
			if j < len(frac) {
				nanos += int(frac[j] - '0')
			}
		}
		rest = rest[i:]
	}
	offset := 0
	switch {
	case rest == "Z" || rest == "z":
	case len(rest) == 6 && (rest[0] == '+' || rest[0] == '-') && rest[3] == ':':
		oh, ok := digits(len(s)-5, len(s)-3)
		om, ok2 := digits(len(s)-2, len(s))
		if !ok || !ok2 || oh > 23 || om > 59 {
			return time.Time{}, bad
		}
		offset = oh*3600 + om*60
		if rest[0] == '-' {
			offset = -offset
		}
	default:
		return time.Time{}, bad
	}
	if year < 1 || month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, bad
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.UTC)
	if t.Day() != day {
		return time.Time{}, bad
	}
	return t.Add(-time.Duration(offset) * time.Second).UTC(), nil
}

// formatInstant writes an instant as UTC with milliseconds, as every instant the SDK sends.
func formatInstant(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// checkInstant refuses an instant outside RFC 3339's years.
func checkInstant(t time.Time, message string) (string, error) {
	if y := t.UTC().Year(); y < 1 || y > 9999 {
		return "", argError(message)
	}
	return formatInstant(t), nil
}
