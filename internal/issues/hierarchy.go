package issues

func IsValidCategory(c Category) bool {
	switch c {
	case CategoryTask, CategoryWorkstream, CategoryProject:
		return true
	default:
		return false
	}
}

func expectedParentCategory(c Category) (Category, bool) {
	switch c {
	case CategoryTask:
		return CategoryWorkstream, true
	case CategoryWorkstream:
		return CategoryProject, true
	case CategoryProject:
		return "", false
	default:
		return "", false
	}
}
