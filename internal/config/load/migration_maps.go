package load

func valueOr(values map[string]any, key string, fallback any) any {
	if value, present := values[key]; present {
		return value
	}
	return fallback
}

func pickMap(raw map[string]any, keys []string) map[string]any {
	out := make(map[string]any)
	for _, key := range keys {
		if value, present := raw[key]; present {
			out[key] = value
		}
	}
	return out
}

func hasAny(raw map[string]any, keys []string) bool {
	for _, key := range keys {
		if _, present := raw[key]; present {
			return true
		}
	}
	return false
}

func copyRecord(raw any) map[string]any {
	record, _ := asRecord(raw)
	out := make(map[string]any, len(record))
	for key, value := range record {
		out[key] = value
	}
	return out
}
