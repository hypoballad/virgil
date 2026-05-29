package tools

import "strings"

const OmittedToolArgumentMarker = "[large tool argument omitted before LLM resend]"

func ContainsOmittedToolArgument(value interface{}) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, OmittedToolArgumentMarker)
	case []string:
		for _, item := range v {
			if ContainsOmittedToolArgument(item) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if ContainsOmittedToolArgument(item) {
				return true
			}
		}
	case map[string]interface{}:
		for _, item := range v {
			if ContainsOmittedToolArgument(item) {
				return true
			}
		}
	}
	return false
}

func OmittedToolArgumentError() string {
	return "tool argument contains an internal omitted-content placeholder; refusing to execute. Re-read the needed source and provide the real argument content."
}
