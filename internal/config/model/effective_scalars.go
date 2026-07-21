package model

func boolOr(p *bool, d bool) bool {
	if p != nil {
		return *p
	}
	return d
}

func intOr(p *int, d int) int {
	if p != nil {
		return *p
	}
	return d
}

func strOr(p *string, d string) string {
	if p != nil {
		return *p
	}
	return d
}
