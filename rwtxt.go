package rwtxt

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/schollz/logger"

	"argc.in/scratch/pkg/db"
	"argc.in/scratch/pkg/markdown"
	"argc.in/scratch/pkg/utils"
)

type RWTxt struct {
	Config     Config
	templates  *template.Template
	fs         *db.FileSystem
	markdown   *markdown.Parser
	wsupgrader websocket.Upgrader
}

type Config struct {
	Bind            string // interface:port to listen on, defaults to DefaultBind.
	Private         bool
	ResizeWidth     int
	ResizeOnUpload  bool
	ResizeOnRequest bool
	OrderByCreated  bool
}

func New(fs *db.FileSystem, config Config) *RWTxt {
	funcMap := template.FuncMap{
		"replace": replace,
	}

	return &RWTxt{
		Config: config,
		fs:     fs,
		wsupgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		markdown:  markdown.NewParser(),
		templates: template.Must(template.New("scratch").Funcs(funcMap).ParseFS(_templates, "templates/*.html")),
	}
}

func (rwt *RWTxt) Serve() (err error) {
	log.Infof("listening on %v", rwt.Config.Bind)
	http.HandleFunc("/", rwt.Handler)
	return http.ListenAndServe(rwt.Config.Bind, nil)
}

func (rwt *RWTxt) isSignedIn(w http.ResponseWriter, r *http.Request, domain string) (signedin bool, domainkey string, defaultDomain string, domainList []string, domainKeys map[string]string) {
	domainKeys, defaultDomain = rwt.getDomainListCookie(w, r)
	domainList = make([]string, len(domainKeys))
	i := 0
	for domainName := range domainKeys {
		domainList[i] = domainName
		i++
		if domain == domainName {
			signedin = true
			domainkey = domainKeys[domainName]
		}
	}
	sort.Strings(domainList)
	return
}

func (rwt *RWTxt) getDomainListCookie(w http.ResponseWriter, r *http.Request) (domainKeys map[string]string, defaultDomain string) {
	startTime := time.Now().UTC()
	domainKeys = make(map[string]string)
	cookie, cookieErr := r.Cookie("rwtxt-domains")
	keysToUpdate := []string{}
	if cookieErr == nil {
		log.Debugf("got cookie: %s", cookie.Value)
		for _, key := range strings.Split(cookie.Value, ",") {
			startTime2 := time.Now().UTC()
			_, domainName, domainErr := rwt.fs.CheckKey(key)
			log.Debugf("checked key: %s [%s]", key, time.Since(startTime2))
			if domainErr == nil && domainName != "" {
				if defaultDomain == "" {
					defaultDomain = domainName
				}
				domainKeys[domainName] = key
				keysToUpdate = append(keysToUpdate, key)
			}
		}
	}
	domainKeys["public"] = ""
	if defaultDomain == "" {
		defaultDomain = "public"
	}
	log.Debugf("logged in domains: %+v [%s]", domainKeys, time.Since(startTime))
	go func() {
		if err := rwt.fs.UpdateKeys(keysToUpdate); err != nil {
			log.Debug(err)
		}
	}()
	return
}

func (rwt *RWTxt) Handler(w http.ResponseWriter, r *http.Request) {
	t := time.Now().UTC()
	err := rwt.Handle(w, r)
	if err != nil {
		log.Error(err)
	}
	log.Infof("%v %v %v %s", r.RemoteAddr, r.Method, r.URL.Path, time.Since(t))
}

func (rwt *RWTxt) Handle(w http.ResponseWriter, r *http.Request) (err error) {

	// very special paths
	if r.URL.Path == "/robots.txt" {
		// special path
		w.Write([]byte(`User-agent: * 
Disallow: /`))
		return
	} else if r.URL.Path == "/favicon.ico" {
		// TODO
	} else if r.URL.Path == "/sitemap.xml" {
		// TODO
	} else if strings.HasPrefix(r.URL.Path, "/static") {
		// special path /static
		return rwt.handleStatic(w, r)
	}

	fields := strings.Split(r.URL.Path, "/")

	tr := NewTemplateRender(rwt)
	tr.Domain = "public"
	if len(fields) > 2 {
		tr.Page = strings.TrimSpace(strings.ToLower(fields[2]))
	}
	if len(fields) > 1 {
		tr.Domain = strings.TrimSpace(strings.ToLower(fields[1]))
	}

	tr.SignedIn, tr.DomainKey, tr.DefaultDomain, tr.DomainList, tr.DomainKeys = rwt.isSignedIn(w, r, tr.Domain)

	// get browser local time
	tr.getUTCOffsetFromCookie(r)

	if r.URL.Path == "/" {
		// special path /
		http.Redirect(w, r, "/"+tr.DefaultDomain, 302)
	} else if r.URL.Path == "/login" {
		// special path /login
		return tr.handleLogin(w, r)
	} else if r.URL.Path == "/ws" {
		// special path /ws
		return tr.handleWebsocket(w, r)
	} else if r.URL.Path == "/update" {
		// special path /login
		return tr.handleLoginUpdate(w, r)
	} else if r.URL.Path == "/logout" {
		// special path /logout
		return tr.handleLogout(w, r)
	} else if r.URL.Path == "/upload" {
		// special path /upload
		return tr.handleUpload(w, r)
	} else if tr.Page == "new" {
		// special path /upload
		http.Redirect(w, r, "/"+tr.DefaultDomain+"/"+rwt.createPage(tr.DefaultDomain).ID, 302)
		return
	} else if strings.HasPrefix(r.URL.Path, "/uploads") {
		// special path /uploads
		return tr.handleUploads(w, r, tr.Page)
	} else if tr.Domain != "" && tr.Page == "" {
		if r.URL.Query().Get("q") != "" {
			if tr.Domain == "public" && !rwt.Config.Private {
				err = fmt.Errorf("cannot search public")
				http.Redirect(w, r, "/"+tr.Domain+"?m="+base64.URLEncoding.EncodeToString([]byte(err.Error())), 302)
				return
			}
			return tr.handleSearch(w, r, tr.Domain, r.URL.Query().Get("q"))
		}
		// domain exists, handle normally
		return tr.handleMain(w, r)
	} else if tr.Domain != "" && tr.Page != "" {
		log.Debugf("[%s/%s]", tr.Domain, tr.Page)
		if tr.Page == "list" {
			if tr.Domain == "public" && !rwt.Config.Private {
				err = fmt.Errorf("cannot list public")
				http.Redirect(w, r, "/"+tr.Domain+"?m="+base64.URLEncoding.EncodeToString([]byte(err.Error())), 302)
				return
			}

			files, _ := rwt.fs.GetAll(tr.Domain, tr.RWTxtConfig.OrderByCreated)
			for i := range files {
				files[i].Data = ""
				files[i].DataHTML = template.HTML("")
			}
			return tr.handleList(w, r, "All", files)
		} else if tr.Page == "export" {
			return tr.handleExport(w, r)
		}
		return tr.handleViewEdit(w, r)
	}
	return
}

func (rwt *RWTxt) handleStatic(w http.ResponseWriter, r *http.Request) (err error) {
	http.FileServer(http.FS(_static)).ServeHTTP(w, r)
	return nil
}

// createPage throws error if domain does not exist
func (rwt *RWTxt) createPage(domain string) (f db.File) {
	f = db.File{
		ID:       utils.UUID(),
		Created:  time.Now().UTC(),
		Domain:   domain,
		Modified: time.Now().UTC(),
	}
	err := rwt.fs.Save(f)
	if err != nil {
		log.Debug(err)
	}
	return
}
