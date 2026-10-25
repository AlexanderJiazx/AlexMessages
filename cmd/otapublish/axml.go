package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"
)

// Readers for the two binary files of an APK that the artifact checks of D10
// need: AndroidManifest.xml (Android's binary XML) and resources.arsc (the
// resource table, to resolve @string/… references). The formats are those of
// frameworks/base/libs/androidfw/include/androidfw/ResourceTypes.h; only
// what an APK built by Gradle or aapt2 contains is read.

const (
	resStringPoolType   = 0x0001
	resTableType        = 0x0002
	resXMLType          = 0x0003
	resXMLStartElement  = 0x0102
	resXMLResourceMap   = 0x0180
	resTablePackageType = 0x0200
	resTableTypeType    = 0x0201

	stringPoolUTF8 = 0x100

	// Res_value data types.
	typeReference = 0x01
	typeString    = 0x03
	typeIntDec    = 0x10
	typeIntHex    = 0x11
	typeBoolean   = 0x12

	noIndex = 0xffffffff

	// Attribute resource ids of the android namespace.
	attrName        = 0x01010003
	attrValue       = 0x01010024
	attrResource    = 0x01010025
	attrVersionCode = 0x0101021b
	attrVersionName = 0x0101021c
)

var errTruncated = errors.New("truncated")

// chunk is a ResChunk_header and its bytes.
type chunk struct {
	typ        uint16
	headerSize int
	b          []byte // the whole chunk
}

func readChunk(b []byte, off int) (chunk, error) {
	if off < 0 || off+8 > len(b) {
		return chunk{}, errTruncated
	}
	typ := binary.LittleEndian.Uint16(b[off:])
	hs := int(binary.LittleEndian.Uint16(b[off+2:]))
	size := int(binary.LittleEndian.Uint32(b[off+4:]))
	if hs < 8 || size < hs || off+size > len(b) {
		return chunk{}, fmt.Errorf("chunk 0x%04x at %d has header %d and size %d in %d bytes", typ, off, hs, size, len(b)-off)
	}
	return chunk{typ: typ, headerSize: hs, b: b[off : off+size]}, nil
}

// children are the chunks that follow c's header.
func (c chunk) children() ([]chunk, error) {
	var out []chunk
	for off := c.headerSize; off < len(c.b); {
		ch, err := readChunk(c.b, off)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
		off += len(ch.b)
	}
	return out, nil
}

func u16(b []byte, off int) (uint16, error) {
	if off < 0 || off+2 > len(b) {
		return 0, errTruncated
	}
	return binary.LittleEndian.Uint16(b[off:]), nil
}

func u32(b []byte, off int) (uint32, error) {
	if off < 0 || off+4 > len(b) {
		return 0, errTruncated
	}
	return binary.LittleEndian.Uint32(b[off:]), nil
}

// parseStringPool reads a ResStringPool chunk (UTF-8 or UTF-16 strings;
// styles are ignored).
func parseStringPool(c chunk) ([]string, error) {
	if c.typ != resStringPoolType || c.headerSize < 28 {
		return nil, fmt.Errorf("not a string pool (chunk 0x%04x)", c.typ)
	}
	b := c.b
	count := int(binary.LittleEndian.Uint32(b[8:]))
	flags := binary.LittleEndian.Uint32(b[16:])
	start := int(binary.LittleEndian.Uint32(b[20:]))
	if count > len(b)/4 || start > len(b) {
		return nil, fmt.Errorf("string pool: %d strings starting at %d in %d bytes", count, start, len(b))
	}
	out := make([]string, count)
	for i := range out {
		o, err := u32(b, c.headerSize+4*i)
		if err != nil {
			return nil, err
		}
		p := start + int(o)
		var s string
		if flags&stringPoolUTF8 != 0 {
			s, err = utf8PoolString(b, p)
		} else {
			s, err = utf16PoolString(b, p)
		}
		if err != nil {
			return nil, fmt.Errorf("string %d: %w", i, err)
		}
		out[i] = s
	}
	return out, nil
}

// utf8Len reads a UTF-8 pool length: one byte, or two when the first has
// its high bit set.
func utf8Len(b []byte, p int) (int, int, error) {
	if p >= len(b) || p < 0 {
		return 0, 0, errTruncated
	}
	if b[p]&0x80 == 0 {
		return int(b[p]), p + 1, nil
	}
	if p+1 >= len(b) {
		return 0, 0, errTruncated
	}
	return int(b[p]&0x7f)<<8 | int(b[p+1]), p + 2, nil
}

func utf8PoolString(b []byte, p int) (string, error) {
	_, p, err := utf8Len(b, p) // the length in characters
	if err != nil {
		return "", err
	}
	n, p, err := utf8Len(b, p) // the length in bytes
	if err != nil {
		return "", err
	}
	if p+n > len(b) {
		return "", errTruncated
	}
	return string(b[p : p+n]), nil
}

func utf16PoolString(b []byte, p int) (string, error) {
	v, err := u16(b, p)
	if err != nil {
		return "", err
	}
	n, p := int(v), p+2
	if v&0x8000 != 0 {
		w, err := u16(b, p)
		if err != nil {
			return "", err
		}
		n, p = int(v&0x7fff)<<16|int(w), p+2
	}
	if p+2*n > len(b) {
		return "", errTruncated
	}
	units := make([]uint16, n)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[p+2*i:])
	}
	return string(utf16.Decode(units)), nil
}

// resValue is a Res_value: a data type and its 32 bits.
type resValue struct {
	typ  uint8
	data uint32
}

// axmlAttr is one attribute of a binary XML element.
type axmlAttr struct {
	name  string
	id    uint32 // the attribute's resource id, 0 when it has none
	raw   string // the raw string value, when there is one
	value resValue
	str   string // the string, when value.typ is typeString
}

// axmlElement is a start tag of a binary XML document. path is the element
// names from the root, joined by "/".
type axmlElement struct {
	path  string
	attrs []axmlAttr
}

// attr finds an attribute by resource id, or by name for attributes outside
// the android namespace (package).
func (e *axmlElement) attr(id uint32, name string) *axmlAttr {
	for i := range e.attrs {
		a := &e.attrs[i]
		if (id != 0 && a.id == id) || (id == 0 && a.id == 0 && a.name == name) {
			return a
		}
	}
	return nil
}

// parseAXML reads Android's binary XML into its start tags, in order.
func parseAXML(b []byte) ([]axmlElement, error) {
	root, err := readChunk(b, 0)
	if err != nil {
		return nil, err
	}
	if root.typ != resXMLType {
		return nil, fmt.Errorf("not binary XML (chunk 0x%04x)", root.typ)
	}
	kids, err := root.children()
	if err != nil {
		return nil, err
	}
	var pool []string
	var resMap []uint32
	str := func(i uint32) (string, error) {
		if i == noIndex {
			return "", nil
		}
		if int(i) >= len(pool) {
			return "", fmt.Errorf("string index %d of %d", i, len(pool))
		}
		return pool[i], nil
	}
	var out []axmlElement
	var stack []string
	for _, c := range kids {
		switch c.typ {
		case resStringPoolType:
			if pool, err = parseStringPool(c); err != nil {
				return nil, err
			}
		case resXMLResourceMap:
			for o := c.headerSize; o+4 <= len(c.b); o += 4 {
				resMap = append(resMap, binary.LittleEndian.Uint32(c.b[o:]))
			}
		case resXMLStartElement:
			e, name, err := parseStartElement(c, str, resMap)
			if err != nil {
				return nil, err
			}
			stack = append(stack, name)
			e.path = joinPath(stack)
			out = append(out, e)
		case resXMLStartElement + 1: // end element
			if len(stack) == 0 {
				return nil, errors.New("an end tag without a start tag")
			}
			stack = stack[:len(stack)-1]
		}
	}
	return out, nil
}

func joinPath(names []string) string {
	p := ""
	for i, n := range names {
		if i > 0 {
			p += "/"
		}
		p += n
	}
	return p
}

func parseStartElement(c chunk, str func(uint32) (string, error), resMap []uint32) (axmlElement, string, error) {
	ext := c.headerSize // ResXMLTree_attrExt
	nameIdx, err := u32(c.b, ext+4)
	if err != nil {
		return axmlElement{}, "", err
	}
	name, err := str(nameIdx)
	if err != nil {
		return axmlElement{}, "", err
	}
	start, err1 := u16(c.b, ext+8)
	size, err2 := u16(c.b, ext+10)
	count, err3 := u16(c.b, ext+12)
	if err := errors.Join(err1, err2, err3); err != nil {
		return axmlElement{}, "", err
	}
	if size < 20 {
		return axmlElement{}, "", fmt.Errorf("<%s>: attribute size %d", name, size)
	}
	var e axmlElement
	for i := 0; i < int(count); i++ {
		o := ext + int(start) + i*int(size)
		if o+20 > len(c.b) {
			return axmlElement{}, "", fmt.Errorf("<%s>: %w", name, errTruncated)
		}
		an := binary.LittleEndian.Uint32(c.b[o+4:])
		raw := binary.LittleEndian.Uint32(c.b[o+8:])
		a := axmlAttr{value: resValue{typ: c.b[o+15], data: binary.LittleEndian.Uint32(c.b[o+16:])}}
		if a.name, err = str(an); err != nil {
			return axmlElement{}, "", err
		}
		if int(an) < len(resMap) {
			a.id = resMap[an]
		}
		if a.raw, err = str(raw); err != nil {
			return axmlElement{}, "", err
		}
		if a.value.typ == typeString {
			if a.str, err = str(a.value.data); err != nil {
				return axmlElement{}, "", err
			}
		}
		e.attrs = append(e.attrs, a)
	}
	return e, name, nil
}

// resTable is the part of resources.arsc that resolves a resource id to its
// value in the default configuration.
type resTable struct {
	strings []string
	// values by resource id (0xPPTTEEEE), from the default configuration,
	// else from the first configuration that has the entry.
	values map[uint32]resValue
}

const (
	typeFlagSparse   = 0x01
	typeFlagOffset16 = 0x02
	entryFlagComplex = 0x0001
	entryFlagCompact = 0x0008
)

func parseARSC(b []byte) (*resTable, error) {
	root, err := readChunk(b, 0)
	if err != nil {
		return nil, err
	}
	if root.typ != resTableType {
		return nil, fmt.Errorf("not a resource table (chunk 0x%04x)", root.typ)
	}
	kids, err := root.children()
	if err != nil {
		return nil, err
	}
	t := &resTable{values: map[uint32]resValue{}}
	fromDefault := map[uint32]bool{}
	for _, c := range kids {
		switch c.typ {
		case resStringPoolType:
			if t.strings, err = parseStringPool(c); err != nil {
				return nil, err
			}
		case resTablePackageType:
			if c.headerSize < 284 {
				return nil, fmt.Errorf("package header of %d bytes", c.headerSize)
			}
			pkg := binary.LittleEndian.Uint32(c.b[8:])
			sub, err := c.children()
			if err != nil {
				return nil, err
			}
			for _, tc := range sub {
				if tc.typ != resTableTypeType {
					continue
				}
				if err := t.readType(tc, pkg, fromDefault); err != nil {
					return nil, err
				}
			}
		}
	}
	return t, nil
}

// readType reads the simple entries of one ResTable_type chunk.
func (t *resTable) readType(c chunk, pkg uint32, fromDefault map[uint32]bool) error {
	if c.headerSize < 20+4 {
		return fmt.Errorf("type chunk header of %d bytes", c.headerSize)
	}
	typeID := uint32(c.b[8])
	flags := c.b[9]
	count := int(binary.LittleEndian.Uint32(c.b[12:]))
	entriesStart := int(binary.LittleEndian.Uint32(c.b[16:]))
	cfgSize := int(binary.LittleEndian.Uint32(c.b[20:]))
	if 20+cfgSize > c.headerSize || cfgSize < 4 {
		return fmt.Errorf("type chunk configuration of %d bytes", cfgSize)
	}
	isDefault := true
	for _, x := range c.b[24 : 20+cfgSize] {
		isDefault = isDefault && x == 0
	}
	entry := func(idx int, off int) error {
		p := entriesStart + off
		size, err1 := u16(c.b, p)
		eflags, err2 := u16(c.b, p+2)
		if err := errors.Join(err1, err2); err != nil {
			return err
		}
		var v resValue
		switch {
		case eflags&entryFlagCompact != 0:
			d, err := u32(c.b, p+4)
			if err != nil {
				return err
			}
			v = resValue{typ: uint8(eflags >> 8), data: d}
		case eflags&entryFlagComplex != 0:
			return nil // a bag (style, array, plural): not a single value
		default:
			if p+int(size)+8 > len(c.b) {
				return errTruncated
			}
			v = resValue{typ: c.b[p+int(size)+3], data: binary.LittleEndian.Uint32(c.b[p+int(size)+4:])}
		}
		id := pkg<<24 | typeID<<16 | uint32(idx)
		if _, ok := t.values[id]; !ok || (isDefault && !fromDefault[id]) {
			t.values[id] = v
			fromDefault[id] = isDefault
		}
		return nil
	}
	for i := 0; i < count; i++ {
		o := c.headerSize
		var idx, off int
		switch {
		case flags&typeFlagSparse != 0:
			x, err1 := u16(c.b, o+4*i)
			y, err2 := u16(c.b, o+4*i+2)
			if err := errors.Join(err1, err2); err != nil {
				return err
			}
			idx, off = int(x), int(y)*4
		case flags&typeFlagOffset16 != 0:
			x, err := u16(c.b, o+2*i)
			if err != nil {
				return err
			}
			if x == 0xffff {
				continue
			}
			idx, off = i, int(x)*4
		default:
			x, err := u32(c.b, o+4*i)
			if err != nil {
				return err
			}
			if x == noIndex {
				continue
			}
			idx, off = i, int(x)
		}
		if err := entry(idx, off); err != nil {
			return fmt.Errorf("resource 0x%02x%02x%04x: %w", pkg, typeID, idx, err)
		}
	}
	return nil
}

// resolve follows references from v to a value that is not a reference.
func (t *resTable) resolve(v resValue) (resValue, error) {
	for i := 0; v.typ == typeReference; i++ {
		if i == 8 || t == nil {
			return v, fmt.Errorf("cannot resolve the reference @0x%08x", v.data)
		}
		next, ok := t.values[v.data]
		if !ok {
			return v, fmt.Errorf("the resource table has no simple value for @0x%08x", v.data)
		}
		v = next
	}
	return v, nil
}

// metaValue is a <meta-data> android:value as Android's PackageParser puts
// it into the application's metaData Bundle: a string, a bool or an int64
// after references are resolved.
func (t *resTable) metaValue(a *axmlAttr) (any, error) {
	v, err := t.resolve(a.value)
	if err != nil {
		return nil, err
	}
	switch {
	case v.typ == typeString && a.value.typ == typeString:
		return a.str, nil
	case v.typ == typeString:
		if int(v.data) >= len(t.strings) {
			return nil, fmt.Errorf("string index %d of %d", v.data, len(t.strings))
		}
		return t.strings[v.data], nil
	case v.typ == typeBoolean:
		return v.data != 0, nil
	case v.typ >= typeIntDec && v.typ <= 0x1f:
		return int64(int32(v.data)), nil
	}
	return nil, fmt.Errorf("a value of type 0x%02x", v.typ)
}
