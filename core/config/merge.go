package config

import (
	"reflect"
)

func DeepMerge(dst, src any) {
	dstVal := reflect.ValueOf(dst)
	srcVal := reflect.ValueOf(src)

	if dstVal.Kind() != reflect.Ptr || srcVal.Kind() != reflect.Ptr {
		return
	}

	mergeValues(dstVal.Elem(), srcVal.Elem())
}

func mergeValues(dst, src reflect.Value) {
	if !dst.CanSet() || !src.IsValid() {
		return
	}

	switch dst.Kind() {
	case reflect.Struct:
		mergeStruct(dst, src)
	case reflect.Map:
		mergeMap(dst, src)
	case reflect.Slice:
		mergeSlice(dst, src)
	default:
		mergeScalar(dst, src)
	}
}

func mergeStruct(dst, src reflect.Value) {
	for i := 0; i < dst.NumField(); i++ {
		dstField := dst.Field(i)
		srcField := src.Field(i)
		mergeValues(dstField, srcField)
	}
}

func mergeMap(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}

	if dst.IsNil() {
		dst.Set(reflect.MakeMap(dst.Type()))
	}

	for _, key := range src.MapKeys() {
		srcVal := src.MapIndex(key)
		dstVal := dst.MapIndex(key)

		if !dstVal.IsValid() {
			dst.SetMapIndex(key, srcVal)
			continue
		}

		if srcVal.Kind() == reflect.Map || srcVal.Kind() == reflect.Struct {
			newDst := reflect.New(dstVal.Type()).Elem()
			newDst.Set(dstVal)
			mergeValues(newDst, srcVal)
			dst.SetMapIndex(key, newDst)
		} else {
			dst.SetMapIndex(key, srcVal)
		}
	}
}

func mergeSlice(dst, src reflect.Value) {
	if src.Len() > 0 {
		dst.Set(src)
	}
}

func mergeScalar(dst, src reflect.Value) {
	if isZeroValue(dst) || !isZeroValue(src) {
		dst.Set(src)
	}
}

func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}
