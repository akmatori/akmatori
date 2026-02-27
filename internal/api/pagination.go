package api

import (
	"net/http"
	"strconv"
)

const (
	defaultPage    = 1
	defaultPerPage = 50
	maxPerPage     = 200
)

// PaginationParams holds parsed pagination query parameters.
type PaginationParams struct {
	Page    int
	PerPage int
}

// ParsePagination extracts pagination parameters from the request.
// Defaults: page=1, per_page=50. Maximum per_page is 200.
func ParsePagination(r *http.Request) PaginationParams {
	p := PaginationParams{
		Page:    defaultPage,
		PerPage: defaultPerPage,
	}

	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.Page = n
		}
	}

	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.PerPage = n
			if p.PerPage > maxPerPage {
				p.PerPage = maxPerPage
			}
		}
	}

	return p
}

// Offset returns the database offset for the current page.
func (p PaginationParams) Offset() int {
	return (p.Page - 1) * p.PerPage
}

// TotalPages calculates the total number of pages for a given total count.
func (p PaginationParams) TotalPages(total int64) int {
	if p.PerPage <= 0 {
		return 0
	}
	pages := int(total) / p.PerPage
	if int(total)%p.PerPage > 0 {
		pages++
	}
	return pages
}
