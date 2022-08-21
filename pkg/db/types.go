package db

import (
	"database/sql"
	"html/template"
	"sync"
	"time"

	"github.com/schollz/versionedtext"
)

type FileSystem struct {
	Name string
	DB   *sql.DB
	sync.RWMutex
}

// File is the basic unit that is saved
type File struct {
	ID       string                      `json:"id"`
	Slug     string                      `json:"slug"`
	Created  time.Time                   `json:"created"`
	Modified time.Time                   `json:"modified"`
	Data     string                      `json:"data"`
	Domain   string                      `json:"domain"`
	History  versionedtext.VersionedText `json:"history"`
	DataHTML template.HTML               `json:"data_html,omitempty"`
	Views    int                         `json:"views"`
}

func (f File) CreatedDate(utcOffset int) string {
	return formattedDate(f.Created, utcOffset)
}

func (f File) ModifiedDate(utcOffset int) string {
	return formattedDate(f.Modified, utcOffset)
}

type DomainOptions struct {
	MostEdited  int
	MostRecent  int
	LastCreated int
	CSS         string
	CustomIntro string
	CustomTitle string
	ShowSearch  bool
}
