package db

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/cihub/seelog"
	"github.com/pkg/errors"
	"github.com/schollz/versionedtext"

	"argc.in/scratch/pkg/utils"
)

// New will initialize a filesystem by creating DB and calling InitializeDB.
// Callers should ensure "github.com/mattn/go-sqlite3" is imported in some way
// before calling this so the sqlite3 driver is available.
func New(name string) (fs *FileSystem, err error) {
	fs = new(FileSystem)
	if name == "" {
		err = errors.New("database must have name")
		return
	}
	fs.Name = name

	fs.DB, err = sql.Open("sqlite3", fs.Name)
	if err != nil {
		return
	}
	err = fs.InitializeDB()
	if err != nil {
		err = errors.Wrap(err, "could not initialize")
		return
	}

	return
}

// InitializeDB will initialize schema if not already done and if dump is true,
// will create the an initial DB dump. This is automatically called by New.
func (fs *FileSystem) InitializeDB() (err error) {
	sqlStmt := `CREATE TABLE IF NOT EXISTS
		fs (
			id TEXT NOT NULL PRIMARY KEY,
			domainid INTEGER,
			slug TEXT,
			created TIMESTAMP,
			modified TIMESTAMP,
			history TEXT,
			views INTEGER DEFAULT 0
		);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating table")
		return
	}

	sqlStmt = `CREATE VIRTUAL TABLE IF NOT EXISTS 
		fts USING fts5 (id,data);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating virtual table")
	}

	sqlStmt = `CREATE TABLE IF NOT EXISTS 
	domains (
		id INTEGER NOT NULL PRIMARY KEY,
		name TEXT,
		hashed_pass TEXT,
		ispublic INTEGER DEFAULT 0,
		options BLOB
	);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating domains table")
	}

	sqlStmt = `CREATE TABLE IF NOT EXISTS
	keys (
		id INTEGER NOT NULL PRIMARY KEY,
		domainid INTEGER,
		key TEXT,
		lastused TIMESTAMP
	);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating keys table")
	}

	sqlStmt = `CREATE TABLE IF NOT EXISTS
	blobs (
		id TEXT NOT NULL PRIMARY KEY,
		name TEXT,
		data BLOB,
		views INTEGER DEFAULT 0
	);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating domains table")
	}

	sqlStmt = `DROP TABLE IF EXISTS	cached_images;`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "dropping cached_images table")
	}

	sqlStmt = `CREATE TABLE IF NOT EXISTS
	cached_images (
		id TEXT NOT NULL PRIMARY KEY,
		name TEXT,
		data BLOB,
		views INTEGER DEFAULT 0
	);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating cached_images table")
	}

	sqlStmt = `DROP TABLE IF EXISTS	cached_html;`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "dropping cached_html table")
	}

	sqlStmt = `CREATE TABLE IF NOT EXISTS
	cached_html (
		id TEXT NOT NULL PRIMARY KEY,
		modified TIMESTAMP,
		tr BLBOB
	);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating cached_html table")
	}

	sqlStmt = `CREATE INDEX IF NOT EXISTS
	fsslugs ON fs(slug,domainid);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating index")
	}

	sqlStmt = `CREATE INDEX IF NOT EXISTS
	domainsname ON domains(name);`
	_, err = fs.DB.Exec(sqlStmt)
	if err != nil {
		err = errors.Wrap(err, "creating index")
	}

	domainid, _, _, _, _ := fs.getDomainFromName("public")
	if domainid == 0 {
		fs.setDomain("public", "")
		fs.UpdateDomain("public", "", true, DomainOptions{})
	}

	return
}

// NewFile returns a new file
func (fs *FileSystem) NewFile(slug, data string) (f File) {
	f = File{
		ID:       utils.UUID(),
		Slug:     slug,
		Created:  time.Now().UTC(),
		Modified: time.Now().UTC(),
		Data:     data,
	}
	return
}

// SaveBlob will save a blob
func (fs *FileSystem) SaveBlob(id string, name string, blob []byte) (err error) {
	fs.Lock()
	defer fs.Unlock()

	tx, err := fs.DB.Begin()
	if err != nil {
		return errors.Wrap(err, "begin SaveBlob")
	}
	stmt, err := tx.Prepare(`
	INSERT OR REPLACE INTO
		blobs
	(
		id,
		name,
		data
	) 
		VALUES 	
	(
		?,
		?,
		?
	)`)
	if err != nil {
		return errors.Wrap(err, "stmt SaveBlob")
	}
	_, err = stmt.Exec(
		id, name, blob,
	)
	if err != nil {
		return errors.Wrap(err, "exec SaveBlob")
	}
	defer stmt.Close()
	err = tx.Commit()
	if err != nil {
		return errors.Wrap(err, "commit SaveBlob")
	}
	return
}

// ExportPosts will save posts to {{TIMESTAMP}}-posts.gz
func (fs *FileSystem) ExportPosts() error {
	domains, err := fs.GetDomains()
	if err != nil {
		return err
	}

	dir := os.TempDir()
	postPaths := []string{}
	for _, domain := range domains {
		files, err := fs.GetAll(domain)
		if err != nil {
			return err
		}
		for _, file := range files {
			fname := fmt.Sprintf("%s-%s.md", file.Slug, file.ID)
			r := strings.NewReader(file.Data)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			_, err = buf.ReadFrom(r)
			if err != nil {
				return err
			}
			err = os.MkdirAll(filepath.Join(dir, domain), os.ModePerm)
			if err != nil {
				return err
			}
			fpath := filepath.Join(dir, domain, fname)
			err = os.WriteFile(fpath, buf.Bytes(), os.ModePerm)
			if err != nil {
				return err
			}

			postPaths = append(postPaths, fpath)
		}
	}
	timestamp := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	for _, f := range postPaths {
		log.Debug(f)
	}
	utils.ZipFiles(fmt.Sprintf("%s-posts.zip", timestamp), postPaths)
	return nil

}

// ExportUploads will save uploads to {{TIMESTAMP}}-uploads.gz
func (fs *FileSystem) ExportUploads() error {
	dir := os.TempDir()
	files := []string{}

	ids, err := fs.GetBlobIDs()
	if err != nil {
		return err
	}

	for _, id := range ids {
		name, data, _, err := fs.GetBlob(id)
		if err != nil {
			return err
		}
		fname := fmt.Sprintf("%s-%s", id, name)

		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		if err != nil {
			return err
		}
		fpath := filepath.Join(dir, fname)
		err = os.WriteFile(fpath, buf.Bytes(), os.ModePerm)
		if err != nil {
			return err
		}

		files = append(files, fpath)
	}

	timestamp := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	for _, f := range files {
		log.Debug(f)
	}
	utils.ZipFiles(fmt.Sprintf("%s-uploads.zip", timestamp), files)
	return nil
}

// GetBlobIDs will return a list of blob ids
func (fs *FileSystem) GetBlobIDs() ([]string, error) {
	fs.Lock()
	defer fs.Unlock()
	stmt, err := fs.DB.Prepare(`SELECT id FROM blobs`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	result := []string{}
	rows, err := stmt.Query()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		err = rows.Scan(&id)
		if err != nil {
			return nil, err
		}
		result = append(result, id)
	}

	return result, nil
}

// GetDomains will return a list of domains
func (fs *FileSystem) GetDomains() ([]string, error) {
	fs.Lock()
	defer fs.Unlock()
	stmt, err := fs.DB.Prepare(`SELECT name FROM domains`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	result := []string{}
	rows, err := stmt.Query()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var domain string
		err = rows.Scan(&domain)
		if err != nil {
			return nil, err
		}
		result = append(result, domain)
	}

	return result, nil
}

// SaveResizedImage will save a resized image
func (fs *FileSystem) SaveResizedImage(id string, name string, blob []byte) (err error) {
	fs.Lock()
	defer fs.Unlock()

	tx, err := fs.DB.Begin()
	if err != nil {
		return errors.Wrap(err, "begin SaveResizedImage")
	}
	stmt, err := tx.Prepare(`
	INSERT OR REPLACE INTO
		cached_images
	(
		id,
		name,
		data
	) 
		VALUES 	
	(
		?,
		?,
		?
	)`)
	if err != nil {
		return errors.Wrap(err, "stmt SaveResizedImage")
	}
	_, err = stmt.Exec(
		id, name, blob,
	)
	if err != nil {
		return errors.Wrap(err, "exec SaveResizedImage")
	}
	defer stmt.Close()
	err = tx.Commit()
	if err != nil {
		return errors.Wrap(err, "commit SaveResizedImage")
	}
	return
}

// GetResizedImage will resize an image (if it hasn't already been cached) return it
func (fs *FileSystem) GetResizedImage(id string) (name string, data []byte, views int, err error) {
	fs.Lock()
	defer fs.Unlock()

	stmt, err := fs.DB.Prepare("SELECT name,data,views FROM cached_images WHERE id = ?")
	if err != nil {
		return
	}
	defer stmt.Close()
	err = stmt.QueryRow(id).Scan(&name, &data, &views)
	if err != nil {
		return
	}

	log.Debugf("id :%s, views: %d", id, views)

	// update the views
	tx, err := fs.DB.Begin()
	if err != nil {
		return
	}
	stmt, err = tx.Prepare("UPDATE blobs SET views=? WHERE id=?")
	if err != nil {
		return
	}
	defer stmt.Close()
	_, err = stmt.Exec(views+1, id)
	if err != nil {
		return
	}
	err = tx.Commit()

	return
}

// GetBlob will save a blob
func (fs *FileSystem) GetBlob(id string) (name string, data []byte, views int, err error) {
	fs.Lock()
	defer fs.Unlock()

	stmt, err := fs.DB.Prepare("SELECT name,data,views FROM blobs WHERE id = ?")
	if err != nil {
		return
	}
	defer stmt.Close()
	err = stmt.QueryRow(id).Scan(&name, &data, &views)
	if err != nil {
		return
	}

	log.Debugf("id :%s, views: %d", id, views)

	// update the views
	tx, err := fs.DB.Begin()
	if err != nil {
		return
	}
	stmt, err = tx.Prepare("UPDATE blobs SET views=? WHERE id=?")
	if err != nil {
		return
	}
	defer stmt.Close()
	_, err = stmt.Exec(views+1, id)
	if err != nil {
		return
	}
	err = tx.Commit()

	return
}

// Save a file to the file system. Will insert or ignore, and then update.
func (fs *FileSystem) Save(f File) (err error) {
	fs.Lock()
	defer fs.Unlock()

	// get current history and then update the history
	files, _ := fs.get(f.ID, f.Domain)
	if len(files) == 1 {
		f.History = files[0].History
		f.History.Update(f.Data)
	} else {
		f.History = versionedtext.NewVersionedText(f.Data)
	}
	// make sure domain exists
	if f.Domain == "" {
		f.Domain = "public"
	}
	domainid, _, _, _, _ := fs.getDomainFromName(f.Domain)
	if domainid == 0 {
		return errors.New("domain does not exist")
	}

	tx, err := fs.DB.Begin()
	if err != nil {
		return errors.Wrap(err, "begin Save")
	}

	stmt, err := tx.Prepare(`
	INSERT OR IGNORE INTO
		fs
	(
		id,
		domainid,
		slug,
		created,
		modified,
		history
	) 
		values 	
	(
		?, 
		?,
		?,
		?,
		?,
		?
	)`)
	if err != nil {
		return errors.Wrap(err, "stmt Save")
	}

	historyBytes, _ := json.Marshal(f.History)

	_, err = stmt.Exec(
		f.ID,
		domainid,
		f.Slug,
		f.Created,
		time.Now().UTC(),
		string(historyBytes),
	)
	if err != nil {
		return errors.Wrap(err, "exec Save")
	}
	defer stmt.Close()
	err = tx.Commit()
	if err != nil {
		return errors.Wrap(err, "commit Save")
	}

	// if it was ignored
	tx2, err := fs.DB.Begin()
	if err != nil {
		return errors.Wrap(err, "begin Save")
	}
	stmt2, err := tx2.Prepare(`
	UPDATE fs SET 
		slug = ?,
		modified = ?,
		history = ?
	WHERE
		id = ?
	`)
	if err != nil {
		return errors.Wrap(err, "stmt update")
	}
	defer stmt2.Close()

	_, err = stmt2.Exec(
		f.Slug,
		time.Now().UTC(),
		string(historyBytes),
		f.ID,
	)
	if err != nil {
		return errors.Wrap(err, "exec update")
	}
	err = tx2.Commit()
	if err != nil {
		return errors.Wrap(err, "commit update")
	}

	// check if exists in fts
	sqlStmt := "INSERT INTO fts(data,id) VALUES (?,?)"
	var ftsHasID bool
	ftsHasID, err = fs.idExists(f.ID)
	if err != nil {
		return errors.Wrap(err, "doesExist")
	}
	if ftsHasID {
		sqlStmt = "UPDATE fts SET data=? WHERE id=?"
	}

	// update the index
	tx3, err := fs.DB.Begin()
	if err != nil {
		return errors.Wrap(err, "begin virtual Save")
	}
	stmt3, err := tx3.Prepare(sqlStmt)
	if err != nil {
		return errors.Wrap(err, "stmt virtual update")
	}
	defer stmt3.Close()

	_, err = stmt3.Exec(
		f.Data,
		f.ID,
	)
	if err != nil {
		return errors.Wrap(err, "exec virtual update")
	}
	err = tx3.Commit()
	if err != nil {
		return errors.Wrap(err, "commit virtual update")
	}
	return

}

// Close will make sure that the lock file is closed
func (fs *FileSystem) Close() (err error) {
	return fs.DB.Close()
}

// SetKey will set the key of a domain, throws an error if it already exists
func (fs *FileSystem) SetKey(domain, password string) (key string, err error) {
	// first check if it is a domain
	fs.Lock()
	defer fs.Unlock()
	domainid, _, err := fs.validateDomain(domain, password)
	if err != nil {
		return
	}
	if domainid == 0 {
		err = errors.New("domain does not exist")
		return
	}
	tx, err := fs.DB.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare("insert into keys(domainid,key,lastused) values(?, ?,?)")
	if err != nil {
		return
	}
	defer stmt.Close()
	key = utils.UUID()
	_, err = stmt.Exec(domainid, key, time.Now().UTC())
	if err != nil {
		return
	}
	err = tx.Commit()
	return
}

func (fs *FileSystem) UpdateViews(f File) (err error) {
	fs.Lock()
	defer fs.Unlock()

	// update the views
	tx, err := fs.DB.Begin()
	if err != nil {
		return
	}
	stmt, err := tx.Prepare("UPDATE fs SET views=? WHERE id=?")
	if err != nil {
		return
	}
	defer stmt.Close()
	_, err = stmt.Exec(f.Views+1, f.ID)
	if err != nil {
		return
	}
	err = tx.Commit()
	return
}

// CheckKey checks that it is a valid key for a domain
func (fs *FileSystem) CheckKey(key string) (domainid int, domain string, err error) {
	fs.Lock()
	defer fs.Unlock()
	stmt, err := fs.DB.Prepare(`
	SELECT 
	domains.id, domains.name
	FROM keys 
	
	INNER JOIN domains 
		ON keys.domainid=domains.id 

	WHERE
		keys.key=?`)
	if err != nil {
		return
	}
	defer stmt.Close()
	err = stmt.QueryRow(key).Scan(&domainid, &domain)
	if err != nil {
		return
	}
	if domain == "" {
		err = errors.New("no such key")
		return
	}

	return
}

// UpdateKeys will update its last use
func (fs *FileSystem) UpdateKeys(keys []string) (err error) {
	fs.Lock()
	defer fs.Unlock()
	tx, err := fs.DB.Begin()
	if err != nil {
		return
	}
	for _, key := range keys {
		stmt, errUpdate := tx.Prepare("UPDATE keys SET lastused=? WHERE key=?")
		if errUpdate != nil {
			err = errUpdate
			return
		}
		defer stmt.Close()
		_, err = stmt.Exec(time.Now().UTC(), key)
		if err != nil {
			return
		}
	}
	err = tx.Commit()
	return
}

// SetDomain will set the key of a domain, throws an error if it already exists
func (fs *FileSystem) SetDomain(domain, password string) (err error) {
	// first check if it is a domain
	fs.Lock()
	defer fs.Unlock()
	domainid, _, _, _, _ := fs.getDomainFromName(domain)
	if domainid != 0 {
		err = errors.New("domain already exists")
		return
	}
	return fs.setDomain(domain, password)
}

func (fs *FileSystem) setDomain(domain, password string) (err error) {
	domain = strings.ToLower(domain)
	tx, err := fs.DB.Begin()
	if err != nil {
		return errors.Wrap(err, "begin Save")
	}

	stmt, err := tx.Prepare(`INSERT INTO domains (name, hashed_pass, ispublic) VALUES (?,?,?)`)
	if err != nil {
		return errors.Wrap(err, "stmt Save")
	}

	hashedPassword, err := utils.HashPassword(password)
	if err != nil {
		return errors.Wrap(err, "can't hash password")
	}
	_, err = stmt.Exec(domain, hashedPassword, 0)
	if err != nil {
		return errors.Wrap(err, "exec Save")
	}
	defer stmt.Close()
	err = tx.Commit()
	if err != nil {
		return errors.Wrap(err, "commit Save")
	}
	return
}

func (fs *FileSystem) UpdateDomain(domain, password string, ispublic bool, options DomainOptions) (err error) {
	fs.Lock()
	defer fs.Unlock()

	// first check if it is a domain
	domainid, _, _, _, _ := fs.getDomainFromName(domain)
	if domainid == 0 {
		err = errors.New("domain does not exist")
		return
	}

	domain = strings.ToLower(domain)
	isPublicValue := 0
	if ispublic {
		isPublicValue = 1
	}

	tx, err := fs.DB.Begin()
	var stmt *sql.Stmt
	if err != nil {
		return errors.Wrap(err, "begin Save")
	}

	bOptions, _ := json.Marshal(options)

	if password == "" {
		stmt, err = tx.Prepare(`UPDATE domains 
		SET 
		ispublic = ?,
		options = ?
		WHERE name = ?`)
		if err != nil {
			return errors.Wrap(err, "stmt Save")
		}
		_, err = stmt.Exec(isPublicValue, bOptions, domain)
		if err != nil {
			return errors.Wrap(err, "exec Save")
		}
	} else {
		hashedPassword, err := utils.HashPassword(password)
		if err != nil {
			return errors.Wrap(err, "can't hash password")
		}
		stmt, err = tx.Prepare(`UPDATE domains 
		SET 
		hashed_pass = ?, 
		ispublic = ?,
		options = ?
		WHERE name = ?`)
		if err != nil {
			return errors.Wrap(err, "stmt Save")
		}
		_, err = stmt.Exec(hashedPassword, isPublicValue, bOptions, domain)
		if err != nil {
			return errors.Wrap(err, "exec Save")
		}
	}
	defer stmt.Close()
	err = tx.Commit()
	if err != nil {
		return errors.Wrap(err, "commit Save")
	}
	return
}

// ValidateDomain returns the domain id or an error if the password doesn't match or if the domain doesn't exist
func (fs *FileSystem) validateDomain(domain, password string) (domainid int, options DomainOptions, err error) {
	domain = strings.ToLower(domain)
	domainid, hashedPassword, _, options, err := fs.getDomainFromName(domain)
	if domainid == 0 {
		err = errors.New("domain " + domain + " does not exist")
		return
	}
	if err != nil {
		return
	}
	err = utils.CheckPasswordHash(hashedPassword, password)
	if err != nil {
		err = errors.New("incorrect password to log into domain")
	}
	return
}

// GetDomainFromName returns the domain id, throwing an error if it doesn't exist
func (fs *FileSystem) GetDomainFromName(domain string) (domainid int, ispublic bool, options DomainOptions, err error) {
	fs.Lock()
	defer fs.Unlock()
	domain = strings.ToLower(domain)
	var ispublicint int
	domainid, _, ispublicint, options, err = fs.getDomainFromName(domain)
	if domainid == 0 {
		err = errors.New("domain " + domain + " does not exist")
	}
	ispublic = ispublicint == 1
	return
}

func (fs *FileSystem) getDomainFromName(domain string) (domainid int, hashedPassword string, ispublic int, options DomainOptions, err error) {
	// prepare statement
	query := "SELECT id,hashed_pass,ispublic,options FROM domains WHERE name = ?"
	stmt, err := fs.DB.Prepare(query)
	if err != nil {
		err = errors.Wrap(err, "preparing query: "+query)
		return
	}

	defer stmt.Close()
	rows, err := stmt.Query(domain)
	if err != nil {
		err = errors.Wrap(err, query)
		return
	}

	// loop through rows
	defer rows.Close()
	for rows.Next() {
		var an_int64 sql.NullInt64
		var b []byte
		err = rows.Scan(&domainid, &hashedPassword, &an_int64, &b)
		if err != nil {
			err = errors.Wrap(err, "getRows")
			return
		}
		ispublic = int(an_int64.Int64)
		json.Unmarshal(b, &options)
	}
	err = rows.Err()
	if err != nil {
		err = errors.Wrap(err, "getRows")
	}
	return
}

// GetAll returns all the files for a given domain
func (fs *FileSystem) GetAll(domain string, created ...bool) (files []File, err error) {
	fs.Lock()
	defer fs.Unlock()
	q := `SELECT fs.id,fs.slug,fs.created,fs.modified,fts.data,fs.history,fs.views FROM fs 
	INNER JOIN fts ON fs.id=fts.id 
	INNER JOIN domains ON fs.domainid=domains.id
	WHERE 
		domains.name = ?
		AND LENGTH(fts.data) > 0
	`
	if len(created) > 0 && created[0] {
		q += "ORDER BY fs.created DESC"
	} else {
		q += "ORDER BY fs.modified DESC"
	}
	files, err = fs.getAllFromPreparedQuery(q, domain)
	for i := range files {
		files[i].Domain = domain
	}
	return
}

// GetTopX returns the info from a file
func (fs *FileSystem) GetTopX(domain string, num int, created ...bool) (files []File, err error) {
	fs.Lock()
	defer fs.Unlock()
	q := `
	SELECT fs.id,fs.slug,fs.created,fs.modified,fts.data,fs.history,fs.views FROM fs 
	INNER JOIN fts ON fs.id=fts.id 
	INNER JOIN domains ON fs.domainid=domains.id
	WHERE 
		domains.name = ?
		AND LENGTH(fts.data) > 0

		`
	if len(created) > 0 && created[0] {
		q += "ORDER BY fs.created DESC"
	} else {
		q += "ORDER BY fs.modified DESC"
	}
	q += " LIMIT ?"
	return fs.getAllFromPreparedQuery(q, domain, num)
}

// GetTopX returns the info from a file
func (fs *FileSystem) GetTopXMostViews(domain string, num int) (files []File, err error) {
	fs.Lock()
	defer fs.Unlock()
	return fs.getAllFromPreparedQuery(`
	SELECT fs.id,fs.slug,fs.created,fs.modified,fts.data,fs.history,fs.views FROM fs 
	INNER JOIN fts ON fs.id=fts.id 
	INNER JOIN domains ON fs.domainid=domains.id
	WHERE 
		domains.name = ?
		AND LENGTH(fts.data) > 0
	ORDER BY fs.views DESC LIMIT ?`, domain, num)
}

// Get returns the info from a file
func (fs *FileSystem) Get(id string, domain string) (files []File, err error) {
	fs.Lock()
	defer fs.Unlock()
	return fs.get(id, domain)
}

func (fs *FileSystem) get(id string, domain string) (files []File, err error) {
	haveID, err := fs.isID(id)
	if err != nil {
		err = errors.Wrap(err, "isID")
		return
	}
	if haveID {
		files, err = fs.getAllFromPreparedQuery(`
		SELECT fs.id,fs.slug,fs.created,fs.modified,fts.data,fs.history,fs.views FROM fs 
		INNER JOIN fts ON fs.id=fts.id 
		WHERE fs.id = ? LIMIT 1`, id)
		if err != nil {
			err = errors.Wrap(err, "get from id")
			return
		}
	} else {
		files, err = fs.getAllFromPreparedQuery(`
		SELECT fs.id,fs.slug,fs.created,fs.modified,fts.data,fs.history,fs.views
		FROM fs 
		INNER JOIN fts ON fs.id=fts.id 
		INNER JOIN domains ON fs.domainid=domains.id
		WHERE 
			fs.id IN (SELECT id FROM fs WHERE slug=?) 
			AND
			domains.name = ?
			ORDER BY modified DESC`, id, domain)
		if err != nil {
			err = errors.Wrap(err, "get from slug")
			return
		}
	}
	if len(files) > 0 {
		return
	}

	err = errors.New("no files with that slug or id")
	return
}

// LastModified get the last modified time
func (fs *FileSystem) LastModified() (lastModified time.Time, err error) {
	// prepare statement
	query := "SELECT modified FROM fs ORDER BY modified DESC LIMIT 1"
	stmt, err := fs.DB.Prepare(query)
	if err != nil {
		err = errors.Wrap(err, "preparing query: "+query)
		return
	}

	defer stmt.Close()
	rows, err := stmt.Query()
	if err != nil {
		err = errors.Wrap(err, query)
		return
	}

	// loop through rows
	defer rows.Close()
	for rows.Next() {
		err = rows.Scan(&lastModified)
		if err != nil {
			err = errors.Wrap(err, "getRows")
			return
		}
	}
	err = rows.Err()
	if err != nil {
		err = errors.Wrap(err, "getRows")
	}
	return
}

// Find returns the info from a file
func (fs *FileSystem) Find(text string, domain string) (files []File, err error) {
	fs.Lock()
	defer fs.Unlock()

	files, err = fs.getAllFromPreparedQuery(`
		SELECT fs.id,fs.slug,fs.created,fs.modified,snippet(fts, 1, '<b>', '</b>', '...', 30),fs.history,fs.views FROM fts 
			INNER JOIN fs ON fs.id=fts.id 
			INNER JOIN domains ON fs.domainid=domains.id
			WHERE fts.data MATCH ?
			AND domains.name = ?
			ORDER BY modified DESC`, text, domain)
	return
}

// Exists returns whether specified ID exists exists
func (fs *FileSystem) idExists(id string) (exists bool, err error) {
	files, err := fs.getAllFromPreparedQuerySingleString(`
		SELECT id FROM fts WHERE id = ?`, id)
	if err != nil {
		err = errors.Wrap(err, "Exists")
	}
	if len(files) > 0 {
		exists = true
	}
	return
}

// isID returns whether specified ID exists exists
func (fs *FileSystem) isID(id string) (exists bool, err error) {
	files, err := fs.getAllFromPreparedQuerySingleString(`
		SELECT id FROM fs WHERE id = ?`, id)
	if err != nil {
		err = errors.Wrap(err, "Exists")
	}
	if len(files) > 0 {
		exists = true
	}
	return
}

// Exists returns whether specified id or slug exists
func (fs *FileSystem) Exists(id string, domain string) (trueID string, many bool, err error) {
	// timeStart := time.Now().UTC()
	// defer func() {
	// 	log.Debugf("checked exists %s/%s in %s", domain, id, time.Since(timeStart))
	// }()

	// fs.Lock()
	// defer fs.Unlock()

	ids, err := fs.getAllFromPreparedQuerySingleString(`
		SELECT id FROM fs WHERE id = ? AND domainid IN (SELECT id FROM domains WHERE name = ?)`, id, domain)
	if err != nil {
		err = errors.Wrap(err, "Exists")
		return
	}
	if len(ids) > 0 {
		trueID = ids[0]
		return
	}

	ids, err = fs.getAllFromPreparedQuerySingleString(`
	SELECT fs.id FROM fs WHERE fs.slug = ? AND fs.domainid IN (SELECT id FROM domains WHERE name = ?)`, id, domain)
	if err != nil {
		err = errors.Wrap(err, "Exists")
		return
	}
	if len(ids) > 0 {
		trueID = ids[0]
	}
	if len(ids) > 1 {
		many = true
	}

	return
}

func (fs *FileSystem) getAllFromPreparedQuery(query string, args ...any) (files []File, err error) {
	// timeStart := time.Now().UTC()
	// defer func() {
	// 	log.Debugf("getAllFromPreparedQuery %s in %s", query, time.Since(timeStart))
	// }()

	// prepare statement
	stmt, err := fs.DB.Prepare(query)
	if err != nil {
		err = errors.Wrap(err, "preparing query: "+query)
		return
	}

	defer stmt.Close()
	rows, err := stmt.Query(args...)
	if err != nil {
		err = errors.Wrap(err, query)
		return
	}

	// loop through rows
	defer rows.Close()
	files = []File{}
	for rows.Next() {
		var f File
		var history sql.NullString
		err = rows.Scan(
			&f.ID,
			&f.Slug,
			&f.Created,
			&f.Modified,
			&f.Data,
			&history,
			&f.Views,
		)
		if err != nil {
			err = errors.Wrap(err, "get rows of file")
			return
		}
		if history.Valid {
			err = json.Unmarshal([]byte(history.String), &f.History)
			if err != nil {
				err = errors.Wrap(err, "could not parse history")
				return
			}
		}
		f.DataHTML = template.HTML(f.Data)
		files = append(files, f)
	}
	err = rows.Err()
	if err != nil {
		err = errors.Wrap(err, "getRows")
	}
	return
}

func (fs *FileSystem) getAllFromPreparedQuerySingleString(query string, args ...interface{}) (s []string, err error) {
	// timeStart := time.Now().UTC()
	// defer func() {
	// 	log.Debugf("getAllFromPreparedQuerySingleString %s in %s", query, time.Since(timeStart))
	// }()

	// prepare statement
	stmt, err := fs.DB.Prepare(query)
	if err != nil {
		err = errors.Wrap(err, "preparing query: "+query)
		return
	}

	defer stmt.Close()
	rows, err := stmt.Query(args...)
	if err != nil {
		err = errors.Wrap(err, query)
		return
	}

	// loop through rows
	defer rows.Close()
	s = []string{}
	for rows.Next() {
		var stemp string
		err = rows.Scan(
			&stemp,
		)
		if err != nil {
			err = errors.Wrap(err, "getRows")
			return
		}
		s = append(s, stemp)
	}
	err = rows.Err()
	if err != nil {
		err = errors.Wrap(err, "getRows")
	}
	return
}

func (fs *FileSystem) getAllFromPreparedQuerySingleTimestamp(query string, args ...interface{}) (s []time.Time, err error) {
	// timeStart := time.Now().UTC()
	// defer func() {
	// 	log.Debugf("getAllFromPreparedQuerySingleTimestamp %s in %s", query, time.Since(timeStart))
	// }()

	// prepare statement
	stmt, err := fs.DB.Prepare(query)
	if err != nil {
		err = errors.Wrap(err, "preparing query: "+query)
		return
	}

	defer stmt.Close()
	rows, err := stmt.Query(args...)
	if err != nil {
		err = errors.Wrap(err, query)
		return
	}

	// loop through rows
	defer rows.Close()
	s = []time.Time{}
	for rows.Next() {
		var stemp time.Time
		err = rows.Scan(
			&stemp,
		)
		if err != nil {
			err = errors.Wrap(err, "getRows")
			return
		}
		s = append(s, stemp)
	}
	err = rows.Err()
	if err != nil {
		err = errors.Wrap(err, "getRows")
	}
	return
}

// setLogLevel determines the log level
func SetLogLevel(level string) (err error) {

	// https://en.wikipedia.org/wiki/ANSI_escape_code#3/4_bit
	// https://github.com/cihub/seelog/wiki/Log-levels
	appConfig := `
	<seelog minlevel="` + level + `">
	<outputs formatid="stdout">
	<filter levels="debug,trace">
		<console formatid="debug"/>
	</filter>
	<filter levels="info">
		<console formatid="info"/>
	</filter>
	<filter levels="critical,error">
		<console formatid="error"/>
	</filter>
	<filter levels="warn">
		<console formatid="warn"/>
	</filter>
	</outputs>
	<formats>
		<format id="stdout"   format="%Date %Time [%LEVEL] %File %FuncShort:%Line %Msg %n" />
		<format id="debug"   format="%Date %Time %EscM(37)[%LEVEL]%EscM(0) %File %FuncShort:%Line %Msg %n" />
		<format id="info"    format="%Date %Time %EscM(36)[%LEVEL]%EscM(0) %File %FuncShort:%Line %Msg %n" />
		<format id="warn"    format="%Date %Time %EscM(33)[%LEVEL]%EscM(0) %File %FuncShort:%Line %Msg %n" />
		<format id="error"   format="%Date %Time %EscM(31)[%LEVEL]%EscM(0) %File %FuncShort:%Line %Msg %n" />
	</formats>
	</seelog>
	`
	logger, err := log.LoggerFromConfigAsBytes([]byte(appConfig))
	if err != nil {
		return
	}
	log.ReplaceLogger(logger)
	return
}

func formattedDate(t time.Time, utcOffset int) string {
	loc, err := time.LoadLocation(fmt.Sprintf("Etc/GMT%+d", utcOffset))
	if err != nil {
		return t.Format("3:04pm Jan 2 2006")
	}
	return t.In(loc).Format("3:04pm Jan 2 2006")
}
