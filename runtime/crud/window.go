package crud

// WindowPolicySpec is the caller-owned set of accepted window bounds.
type WindowPolicySpec struct {
	MinLimit  int64
	MaxLimit  int64
	MaxOffset int64
}

// WindowPolicy is an immutable validated caller policy.
type WindowPolicy struct {
	minLimit  int64
	maxLimit  int64
	maxOffset int64
	valid     bool
}

func NewWindowPolicy(spec WindowPolicySpec) (WindowPolicy, error) {
	if spec.MinLimit < 1 {
		return WindowPolicy{}, windowPolicyInvalid("min_limit_invalid", "/minLimit")
	}
	if spec.MaxLimit < spec.MinLimit {
		return WindowPolicy{}, windowPolicyInvalid("max_limit_invalid", "/maxLimit")
	}
	if spec.MaxOffset < 0 {
		return WindowPolicy{}, windowPolicyInvalid("max_offset_invalid", "/maxOffset")
	}
	return WindowPolicy{
		minLimit:  spec.MinLimit,
		maxLimit:  spec.MaxLimit,
		maxOffset: spec.MaxOffset,
		valid:     true,
	}, nil
}

func (p WindowPolicy) Check(limit, offset int64) (Window, error) {
	if !p.valid {
		return Window{}, windowPolicyInvalid("min_limit_invalid", "/minLimit")
	}
	if limit < p.minLimit || limit > p.maxLimit {
		return Window{}, windowInvalid("limit_out_of_range", "/limit")
	}
	if offset < 0 || offset > p.maxOffset {
		return Window{}, windowInvalid("offset_out_of_range", "/offset")
	}
	return Window{limit: limit, offset: offset}, nil
}

// Window is an immutable caller value accepted by a WindowPolicy.
type Window struct {
	limit  int64
	offset int64
}

func (w Window) Limit() int64 {
	return w.limit
}

func (w Window) Offset() int64 {
	return w.offset
}
