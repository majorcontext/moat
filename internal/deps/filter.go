package deps

// FilterServices returns only service-type dependencies.
func FilterServices(deps []Dependency) []Dependency {
	var result []Dependency
	for _, d := range deps {
		if d.Type == TypeService {
			result = append(result, d)
		}
	}
	return result
}

// FilterInstallable returns dependencies excluding services.
func FilterInstallable(deps []Dependency) []Dependency {
	var result []Dependency
	for _, d := range deps {
		if d.Type != TypeService {
			result = append(result, d)
		}
	}
	return result
}
