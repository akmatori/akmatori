package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePagination_Defaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	p := ParsePagination(r)

	if p.Page != 1 {
		t.Errorf("page = %d, want 1", p.Page)
	}
	if p.PerPage != 50 {
		t.Errorf("per_page = %d, want 50", p.PerPage)
	}
}

func TestParsePagination_CustomValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/test?page=3&per_page=25", nil)
	p := ParsePagination(r)

	if p.Page != 3 {
		t.Errorf("page = %d, want 3", p.Page)
	}
	if p.PerPage != 25 {
		t.Errorf("per_page = %d, want 25", p.PerPage)
	}
}

func TestParsePagination_MaxPerPage(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/test?per_page=500", nil)
	p := ParsePagination(r)

	if p.PerPage != 200 {
		t.Errorf("per_page = %d, want 200 (capped)", p.PerPage)
	}
}

func TestParsePagination_InvalidValues(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantPage    int
		wantPerPage int
	}{
		{"negative page", "page=-1", 1, 50},
		{"zero page", "page=0", 1, 50},
		{"non-numeric page", "page=abc", 1, 50},
		{"negative per_page", "per_page=-5", 1, 50},
		{"zero per_page", "per_page=0", 1, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/test?"+tt.query, nil)
			p := ParsePagination(r)

			if p.Page != tt.wantPage {
				t.Errorf("page = %d, want %d", p.Page, tt.wantPage)
			}
			if p.PerPage != tt.wantPerPage {
				t.Errorf("per_page = %d, want %d", p.PerPage, tt.wantPerPage)
			}
		})
	}
}

func TestPaginationParams_Offset(t *testing.T) {
	tests := []struct {
		name       string
		page       int
		perPage    int
		wantOffset int
	}{
		{"first page", 1, 50, 0},
		{"second page", 2, 50, 50},
		{"third page, 25 per", 3, 25, 50},
		{"large page", 10, 100, 900},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PaginationParams{Page: tt.page, PerPage: tt.perPage}
			if got := p.Offset(); got != tt.wantOffset {
				t.Errorf("Offset() = %d, want %d", got, tt.wantOffset)
			}
		})
	}
}

func TestPaginationParams_TotalPages(t *testing.T) {
	tests := []struct {
		name      string
		perPage   int
		total     int64
		wantPages int
	}{
		{"exact division", 10, 100, 10},
		{"with remainder", 10, 101, 11},
		{"single page", 50, 30, 1},
		{"zero total", 50, 0, 0},
		{"one item", 50, 1, 1},
		{"zero per page", 0, 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PaginationParams{Page: 1, PerPage: tt.perPage}
			if got := p.TotalPages(tt.total); got != tt.wantPages {
				t.Errorf("TotalPages(%d) = %d, want %d", tt.total, got, tt.wantPages)
			}
		})
	}
}
