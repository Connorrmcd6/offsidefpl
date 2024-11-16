package lib

import "strings"

func ReplaceSpacesWithUnderscores(name string) string {
	if name == "" {
		return name
	}
	return strings.ReplaceAll(name, " ", "_")
}

func ReplaceUnderscoresWithSpaces(name string) string {
	if name == "" {
		return name
	}
	return strings.ReplaceAll(name, "_", " ")
}
