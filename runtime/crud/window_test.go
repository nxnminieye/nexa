package crud

import (
	"errors"
	"math"
	"testing"
)

func TestWindowPolicyConstructorValidationOrderAndZeroResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    WindowPolicySpec
		reason  string
		pointer string
	}{
		{
			name:    "minimum wins over other invalid fields",
			spec:    WindowPolicySpec{MinLimit: 0, MaxLimit: -1, MaxOffset: -1},
			reason:  "min_limit_invalid",
			pointer: "/minLimit",
		},
		{
			name:    "minimum int64",
			spec:    WindowPolicySpec{MinLimit: math.MinInt64, MaxLimit: math.MinInt64, MaxOffset: math.MinInt64},
			reason:  "min_limit_invalid",
			pointer: "/minLimit",
		},
		{
			name:    "maximum wins over offset",
			spec:    WindowPolicySpec{MinLimit: 2, MaxLimit: 1, MaxOffset: -1},
			reason:  "max_limit_invalid",
			pointer: "/maxLimit",
		},
		{
			name:    "inverted extreme bounds",
			spec:    WindowPolicySpec{MinLimit: math.MaxInt64, MaxLimit: math.MinInt64, MaxOffset: 0},
			reason:  "max_limit_invalid",
			pointer: "/maxLimit",
		},
		{
			name:    "negative maximum offset",
			spec:    WindowPolicySpec{MinLimit: 1, MaxLimit: 1, MaxOffset: -1},
			reason:  "max_offset_invalid",
			pointer: "/maxOffset",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			policy, err := NewWindowPolicy(test.spec)
			assertCRUDerror(t, err, ErrWindowPolicyInvalid, "window_policy_invalid", test.reason, test.pointer)

			window, checkErr := policy.Check(1, 0)
			assertCRUDerror(t, checkErr, ErrWindowPolicyInvalid, "window_policy_invalid", "min_limit_invalid", "/minLimit")
			assertZeroWindow(t, window)
		})
	}
}

func TestWindowPolicyZeroPolicyWinsBeforeCallerValues(t *testing.T) {
	t.Parallel()

	var policy WindowPolicy
	for _, values := range [][2]int64{
		{0, 0},
		{math.MinInt64, math.MinInt64},
		{math.MaxInt64, math.MaxInt64},
	} {
		window, err := policy.Check(values[0], values[1])
		assertCRUDerror(t, err, ErrWindowPolicyInvalid, "window_policy_invalid", "min_limit_invalid", "/minLimit")
		assertZeroWindow(t, window)
	}
}

func TestWindowPolicyAcceptsInclusiveBoundsAndPreservesValues(t *testing.T) {
	t.Parallel()

	policy, err := NewWindowPolicy(WindowPolicySpec{MinLimit: 2, MaxLimit: 10, MaxOffset: 20})
	if err != nil {
		t.Fatalf("NewWindowPolicy() error = %v", err)
	}
	for _, values := range [][2]int64{{2, 0}, {7, 13}, {10, 20}} {
		window, err := policy.Check(values[0], values[1])
		if err != nil {
			t.Fatalf("Check(%d, %d) error = %v", values[0], values[1], err)
		}
		if window.Limit() != values[0] || window.Offset() != values[1] {
			t.Fatalf("Check(%d, %d) = (%d, %d)", values[0], values[1], window.Limit(), window.Offset())
		}
	}
}

func TestWindowPolicyRejectsLimitBeforeOffsetAndReturnsZeroWindow(t *testing.T) {
	t.Parallel()

	policy, err := NewWindowPolicy(WindowPolicySpec{MinLimit: 2, MaxLimit: 10, MaxOffset: 20})
	if err != nil {
		t.Fatalf("NewWindowPolicy() error = %v", err)
	}
	tests := []struct {
		name    string
		limit   int64
		offset  int64
		reason  string
		pointer string
	}{
		{name: "zero is not defaulted", limit: 0, offset: 0, reason: "limit_out_of_range", pointer: "/limit"},
		{name: "below minimum", limit: 1, offset: 0, reason: "limit_out_of_range", pointer: "/limit"},
		{name: "above maximum", limit: 11, offset: 0, reason: "limit_out_of_range", pointer: "/limit"},
		{name: "minimum int limit wins", limit: math.MinInt64, offset: math.MinInt64, reason: "limit_out_of_range", pointer: "/limit"},
		{name: "negative offset", limit: 2, offset: -1, reason: "offset_out_of_range", pointer: "/offset"},
		{name: "minimum int offset", limit: 2, offset: math.MinInt64, reason: "offset_out_of_range", pointer: "/offset"},
		{name: "offset above maximum", limit: 2, offset: 21, reason: "offset_out_of_range", pointer: "/offset"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			window, err := policy.Check(test.limit, test.offset)
			assertCRUDerror(t, err, ErrWindowInvalid, "window_invalid", test.reason, test.pointer)
			if errors.Is(err, ErrWindowPolicyInvalid) {
				t.Fatalf("errors.Is(%v, ErrWindowPolicyInvalid) = true", err)
			}
			assertZeroWindow(t, window)
		})
	}
}

func TestWindowPolicySupportsInt64ExtremesWithoutOverflow(t *testing.T) {
	t.Parallel()

	policy, err := NewWindowPolicy(WindowPolicySpec{
		MinLimit:  1,
		MaxLimit:  math.MaxInt64,
		MaxOffset: math.MaxInt64,
	})
	if err != nil {
		t.Fatalf("NewWindowPolicy() error = %v", err)
	}
	window, err := policy.Check(math.MaxInt64, math.MaxInt64)
	if err != nil {
		t.Fatalf("Check(MaxInt64, MaxInt64) error = %v", err)
	}
	if window.Limit() != math.MaxInt64 || window.Offset() != math.MaxInt64 {
		t.Fatalf("window = (%d, %d)", window.Limit(), window.Offset())
	}
}

func TestWindowPolicyRemainsCallerSpecific(t *testing.T) {
	t.Parallel()

	strict, err := NewWindowPolicy(WindowPolicySpec{MinLimit: 1, MaxLimit: 5, MaxOffset: 10})
	if err != nil {
		t.Fatalf("NewWindowPolicy(strict) error = %v", err)
	}
	wide, err := NewWindowPolicy(WindowPolicySpec{MinLimit: 10, MaxLimit: 100, MaxOffset: 1_000})
	if err != nil {
		t.Fatalf("NewWindowPolicy(wide) error = %v", err)
	}

	if _, err := strict.Check(10, 11); err == nil {
		t.Fatal("strict.Check() error = nil")
	} else {
		assertCRUDerror(t, err, ErrWindowInvalid, "window_invalid", "limit_out_of_range", "/limit")
	}
	window, err := wide.Check(10, 11)
	if err != nil {
		t.Fatalf("wide.Check() error = %v", err)
	}
	if window.Limit() != 10 || window.Offset() != 11 {
		t.Fatalf("wide.Check() = (%d, %d)", window.Limit(), window.Offset())
	}
}

func TestWindowPolicyZeroWindowAccessors(t *testing.T) {
	t.Parallel()

	assertZeroWindow(t, Window{})
}

func assertZeroWindow(t *testing.T, window Window) {
	t.Helper()
	if window.Limit() != 0 || window.Offset() != 0 {
		t.Fatalf("zero Window = (%d, %d)", window.Limit(), window.Offset())
	}
}
