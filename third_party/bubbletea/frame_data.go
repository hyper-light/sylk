package tea

import (
	"reflect"
	"sort"
)

// FrameData describes a rendered frame plus optional incremental repaint
// metadata. Models can expose this shape directly, or return another struct
// with the same exported fields from a FrameData() method.
type FrameData struct {
	Lines     []string
	DirtyRows []int
	Version   uint64
	Full      bool
}

func extractModelFrameData(model Model) (FrameData, bool) {
	if model == nil {
		return FrameData{}, false
	}
	method := reflect.ValueOf(model).MethodByName("FrameData")
	if !method.IsValid() || method.Type().NumIn() != 0 || method.Type().NumOut() != 1 {
		return FrameData{}, false
	}
	results := method.Call(nil)
	if len(results) != 1 {
		return FrameData{}, false
	}
	return normalizeFrameData(results[0].Interface())
}

func normalizeFrameData(value any) (FrameData, bool) {
	switch typed := value.(type) {
	case nil:
		return FrameData{}, false
	case FrameData:
		return sanitizeFrameData(typed)
	case *FrameData:
		if typed == nil {
			return FrameData{}, false
		}
		return sanitizeFrameData(*typed)
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return FrameData{}, false
	}
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return FrameData{}, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return FrameData{}, false
	}

	linesField := rv.FieldByName("Lines")
	dirtyField := rv.FieldByName("DirtyRows")
	versionField := rv.FieldByName("Version")
	fullField := rv.FieldByName("Full")
	if !linesField.IsValid() || !dirtyField.IsValid() || !versionField.IsValid() || !fullField.IsValid() {
		return FrameData{}, false
	}
	if linesField.Type() != reflect.TypeOf([]string(nil)) || dirtyField.Type() != reflect.TypeOf([]int(nil)) {
		return FrameData{}, false
	}
	if fullField.Kind() != reflect.Bool {
		return FrameData{}, false
	}
	switch versionField.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
	default:
		return FrameData{}, false
	}

	fd := FrameData{
		Lines:     append([]string(nil), linesField.Interface().([]string)...),
		DirtyRows: append([]int(nil), dirtyField.Interface().([]int)...),
		Version:   versionField.Uint(),
		Full:      fullField.Bool(),
	}
	return sanitizeFrameData(fd)
}

func sanitizeFrameData(frame FrameData) (FrameData, bool) {
	if len(frame.Lines) == 0 && len(frame.DirtyRows) == 0 && frame.Version == 0 && !frame.Full {
		return FrameData{}, false
	}
	frame.Lines = append([]string(nil), frame.Lines...)
	frame.DirtyRows = append([]int(nil), frame.DirtyRows...)
	return frame, true
}

func normalizeDirtyRows(rows []int, height int) []int {
	if len(rows) == 0 {
		return nil
	}
	filtered := make([]int, 0, len(rows))
	for _, row := range rows {
		if row < 0 || row >= height {
			continue
		}
		filtered = append(filtered, row)
	}
	if len(filtered) == 0 {
		return nil
	}
	sort.Ints(filtered)
	out := filtered[:1]
	for _, row := range filtered[1:] {
		if row != out[len(out)-1] {
			out = append(out, row)
		}
	}
	return out
}
