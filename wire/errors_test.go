// SPDX-License-Identifier: MIT

package wire

import (
	"errors"
	"testing"
)

func TestS7Error_Error(t *testing.T) {
	e := &S7Error{Class: 0x81, Code: 0x01}
	if e.Error() != "S7 error: class=0x81, code=0x01" {
		t.Fatalf("Error() without message: got %q", e.Error())
	}
	e.Message = "custom"
	if e.Error() != "custom" {
		t.Fatalf("Error() with message: got %q", e.Error())
	}
}

func TestNewS7Error(t *testing.T) {
	e := NewS7Error(ErrClassAccess, 0x00)
	if e == nil || e.Message != "no access rights" {
		t.Fatalf("NewS7Error(access, 0x00): got %v", e)
	}
	e = NewS7Error(ErrClassAccess, 0x01)
	if e.Message != "invalid address" {
		t.Fatalf("NewS7Error(access, 0x01): got %q", e.Message)
	}
	e = NewS7Error(ErrClassAccess, 0x04)
	if e.Message != "invalid data type" {
		t.Fatalf("NewS7Error(access, 0x04): got %q", e.Message)
	}
	e = NewS7Error(ErrClassAccess, 0xFF)
	if e.Message != "access error code=0xFF" {
		t.Fatalf("NewS7Error(access, 0xFF): got %q", e.Message)
	}
	e = NewS7Error(ErrClassObject, 0)
	if e.Message != "object does not exist" {
		t.Fatalf("NewS7Error(object): got %q", e.Message)
	}
	e = NewS7Error(ErrClassResource, 0)
	if e.Message != "resource busy" {
		t.Fatalf("NewS7Error(resource): got %q", e.Message)
	}
	e = NewS7Error(0x00, 0)
	if e.Message != "" {
		t.Fatalf("NewS7Error(no error class): expected empty message, got %q", e.Message)
	}
}

func TestHeaderErrorString(t *testing.T) {
	if got := HeaderErrorString(ErrClassNoError, 0); got != "" {
		t.Errorf("HeaderErrorString(no error): got %q", got)
	}
	if got := HeaderErrorString(ErrClassAccess, 0x01); got != "invalid address" {
		t.Errorf("HeaderErrorString(access, 0x01): got %q", got)
	}
	if got := HeaderErrorString(0xFF, 0xAB); got == "" {
		t.Error("HeaderErrorString(unknown): expected non-empty")
	}
}

func TestErrClassString(t *testing.T) {
	if got := ErrClassString(ErrClassNoError); got != "No error" {
		t.Errorf("ErrClassString(NoError): got %q", got)
	}
	if got := ErrClassString(ErrClassAccess); got != "Access error" {
		t.Errorf("ErrClassString(Access): got %q", got)
	}
	if got := ErrClassString(ErrClassObject); got != "Object definition" {
		t.Errorf("ErrClassString(Object): got %q", got)
	}
	if got := ErrClassString(0x99); got != "class 0x99" {
		t.Errorf("ErrClassString(unknown): got %q", got)
	}
}

func TestItemReturnCodeString(t *testing.T) {
	if got := ItemReturnCodeString(RetCodeSuccess); got != "success" {
		t.Errorf("ItemReturnCodeString(success): got %q", got)
	}
	if got := ItemReturnCodeString(RetCodeAccessFault); got != "access denied" {
		t.Errorf("ItemReturnCodeString(access): got %q", got)
	}
}

func TestNewS7ErrorWithParam(t *testing.T) {
	e := NewS7ErrorWithParam(ErrClassAccess, 0x01, nil)
	if e.Message != "invalid address" {
		t.Errorf("NewS7ErrorWithParam(nil param): message = %q, want header default", e.Message)
	}
	param := []byte{0x00, 0x00, 0x01, 0x14} // big-endian 0x0114 = block not found
	e = NewS7ErrorWithParam(ErrClassObject, 0, param)
	if e.Message != "block not found" {
		t.Errorf("NewS7ErrorWithParam(0x0114): message = %q, want block not found", e.Message)
	}
	shortParam := []byte{0x00, 0x00}
	e = NewS7ErrorWithParam(ErrClassAccess, 0x01, shortParam)
	if e.Message != "invalid address" {
		t.Errorf("NewS7ErrorWithParam(short param): message = %q", e.Message)
	}
}

func TestReturnCodeError(t *testing.T) {
	if err := ReturnCodeError(RetCodeSuccess); err != nil {
		t.Fatalf("RetCodeSuccess should return nil: %v", err)
	}
	codes := []struct {
		code byte
		msg  string
	}{
		{RetCodeHWFault, "hardware fault"},
		{RetCodeAccessFault, "access denied"},
		{RetCodeAddressFault, "invalid address"},
		{RetCodeDataTypeFault, "data type not supported"},
		{RetCodeDataSizeFault, "data size mismatch"},
		{RetCodeBusy, "object busy"},
		{RetCodeNotAvailable, "object not available"},
	}
	for _, c := range codes {
		err := ReturnCodeError(c.code)
		if err == nil {
			t.Fatalf("ReturnCodeError(0x%02X) expected error", c.code)
		}
		if err.Error() != c.msg {
			t.Fatalf("ReturnCodeError(0x%02X): got %q, want %q", c.code, err.Error(), c.msg)
		}
	}
	err := ReturnCodeError(0x99)
	if err == nil || err.Error() != "return code 0x99" {
		t.Fatalf("ReturnCodeError(unknown): got %v", err)
	}
	// S7Error must preserve raw Code for item return codes
	var s7err *S7Error
	if errors.As(err, &s7err) && s7err.Code != 0x99 {
		t.Errorf("S7Error.Code = 0x%02X, want 0x99", s7err.Code)
	}
}
