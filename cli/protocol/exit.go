package protocol

func ExitStatus(err error) int {
	if err == nil {
		return 0
	}

	switch Project(err).Category {
	case CategoryUsage:
		return 2
	case CategoryInput:
		return 3
	case CategoryReview:
		return 5
	case CategoryDrift:
		return 12
	case CategoryConflict:
		return 13
	case CategoryUnavailable:
		return 6
	case CategoryExternal:
		return 7
	case CategoryCanceled:
		return 130
	case CategoryInternal:
		return 70
	default:
		return 70
	}
}
