package model

import "github.com/otfabric/go-s7comm/model/codec"

// Data conversion helpers for S7 types (big-endian). Implementations live in model/codec;
// these wrappers preserve the model package API.

func DecodeBool(data []byte, bit int) bool       { return codec.DecodeBool(data, bit) }
func DecodeByte(data []byte) byte                { return codec.DecodeByte(data) }
func DecodeWord(data []byte) uint16              { return codec.DecodeWord(data) }
func DecodeInt(data []byte) int16                { return codec.DecodeInt(data) }
func DecodeDWord(data []byte) uint32             { return codec.DecodeDWord(data) }
func DecodeDInt(data []byte) int32               { return codec.DecodeDInt(data) }
func DecodeReal(data []byte) float32             { return codec.DecodeReal(data) }
func DecodeString(data []byte) string            { return codec.DecodeString(data) }
func EncodeBool(val bool) []byte                 { return codec.EncodeBool(val) }
func EncodeByte(val byte) []byte                 { return codec.EncodeByte(val) }
func EncodeWord(val uint16) []byte               { return codec.EncodeWord(val) }
func EncodeInt(val int16) []byte                 { return codec.EncodeInt(val) }
func EncodeDWord(val uint32) []byte              { return codec.EncodeDWord(val) }
func EncodeDInt(val int32) []byte                { return codec.EncodeDInt(val) }
func EncodeReal(val float32) []byte              { return codec.EncodeReal(val) }
func EncodeString(val string, maxLen int) []byte { return codec.EncodeString(val, maxLen) }
