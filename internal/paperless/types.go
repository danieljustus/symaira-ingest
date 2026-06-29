package paperless

import "time"

type Document struct {
	ID            int       `json:"id"`
	Title         string    `json:"title"`
	Content       string    `json:"content"`
	CreatedDate   time.Time `json:"created_date"`
	Added         time.Time `json:"added"`
	Modified      time.Time `json:"modified"`
	Correspondent *Ref      `json:"correspondent"`
	Tags          []Ref     `json:"tags"`
	DocumentType  *Ref      `json:"document_type"`
	FileType      string    `json:"file_type"`
	PageCount     int       `json:"page_count"`
}

type Tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Correspondent struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type DocumentType struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Ref struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type listResponse[T any] struct {
	Count   int `json:"count"`
	Results []T `json:"results"`
	Next    string `json:"next"`
}
