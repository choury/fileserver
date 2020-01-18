package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/handlers"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

var listen = flag.String("listen", "127.0.0.1:8080", "listen endpoint")

func main() {
	flag.Parse()
	BasePath = flag.Arg(0)
	mux := http.NewServeMux()
	mux.Handle("/css/", http.FileServer(http.Dir(".")))
	mux.Handle("/js/", http.FileServer(http.Dir(".")))
	mux.Handle("/images/", http.FileServer(http.Dir(".")))
	mux.HandleFunc("/", index)
	mux.HandleFunc("/download", download)
	mux.HandleFunc("/list", list)
	mux.HandleFunc("/img", showimg)
	mux.HandleFunc("/video", showvid)
	mux.HandleFunc("/txt", showtext)
	mux.HandleFunc("/del", del)
	mux.HandleFunc("/upload", upload)
	mux.HandleFunc("/test", hello)
	panic(http.ListenAndServe(*listen, handlers.LoggingHandler(os.Stdout, mux)))
}

func hello(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello\n"))
}

func setsession(w *http.ResponseWriter, name string, value string) {
	cookie := http.Cookie{Name: name, Value: value, Path: "/"}
	http.SetCookie(*w, &cookie)
}

// httpRange specifies the byte range to be sent to the client.
type httpRange struct {
	start, length int64
}

// parseRange parses a Range header string as per RFC 2616.
// errNoOverlap is returned if none of the ranges overlap.
func parseRange(s string, size int64) ([]httpRange, error) {
	if s == "" {
		return nil, nil // header not present
	}
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, errors.New("invalid range")
	}
	var ranges []httpRange
	noOverlap := false
	for _, ra := range strings.Split(s[len(b):], ",") {
		ra = strings.TrimSpace(ra)
		if ra == "" {
			continue
		}
		i := strings.Index(ra, "-")
		if i < 0 {
			return nil, errors.New("invalid range")
		}
		start, end := strings.TrimSpace(ra[:i]), strings.TrimSpace(ra[i+1:])
		var r httpRange
		if start == "" {
			// If no start is specified, end specifies the
			// range start relative to the end of the file.
			i, err := strconv.ParseInt(end, 10, 64)
			if err != nil {
				return nil, errors.New("invalid range")
			}
			if i > size {
				i = size
			}
			r.start = size - i
			r.length = size - r.start
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i < 0 {
				return nil, errors.New("invalid range")
			}
			if i >= size {
				// If the range begins after the size of the content,
				// then it does not overlap.
				noOverlap = true
				continue
			}
			r.start = i
			if end == "" {
				// If no end is specified, range extends to end of the file.
				r.length = size - r.start
			} else {
				i, err := strconv.ParseInt(end, 10, 64)
				if err != nil || r.start > i {
					return nil, errors.New("invalid range")
				}
				if i >= size {
					i = size - 1
				}
				r.length = i - r.start + 1
			}
		}
		ranges = append(ranges, r)
	}
	if noOverlap && len(ranges) == 0 {
		// The specified ranges did not overlap with the content.
		return nil, errors.New("invalid range: failed to overlap")
	}
	return ranges, nil
}

func download(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	path := r.FormValue("path")
	file, err := Stat(path)
	if err != nil {
		fmt.Println("Stat file:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	mtime := file.Mtime
	w.Header().Set("Last-Modified", mtime.Format(http.TimeFormat))
	if since := r.Header.Get("If-Modified-Since"); since != "" {
		sinceTime, _ := time.Parse(http.TimeFormat, since)
		if sinceTime.After(mtime) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	var ranges []httpRange
	if ranges, err = parseRange(r.Header.Get("Range"), file.Size); err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", file.Size))
		http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
		return
	}

	if ranges != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", ranges[0].start, ranges[0].start+ranges[0].length-1, file.Size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		ranges = []httpRange{
			{start: 0, length: file.Size},
		}
	}

	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(file.Path)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", ranges[0].length))
	w.Header().Set("Accept-Ranges", "bytes")

	notify := w.(http.CloseNotifier).CloseNotify()
	out, err := Download(path, ranges[0].start, ranges[0].length, notify)
	if err != nil {
		fmt.Println("Read file:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	for {
		date, ok := <-out
		if !ok {
			return
		}
		w.Write(date)
	}
}

func index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Strict-Transport-Security", "max-age=31536000;")
	if r.URL.Path == "/favicon.ico" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/list", http.StatusFound)
}

type filelist struct {
	Path     string
	PagePath string
	Page     int
	Files    []File
}

var funcMap = template.FuncMap{
	"Inc": func(p int) int {
		return p + 1
	},
	"Dec": func(p int) int {
		return p - 1
	},
	"IncPage": func(PagePath string) string {
		dir := filepath.Dir(PagePath)
		base := filepath.Base(PagePath)
		page, _ := strconv.Atoi(base)
		page++
		return filepath.Join(dir, strconv.Itoa(page))
	},
	"DecPage": func(PagePath string) string {
		dir := filepath.Dir(PagePath)
		base := filepath.Base(PagePath)
		page, _ := strconv.Atoi(base)
		page--
		return filepath.Join(dir, strconv.Itoa(page))
	},
	"Basename": filepath.Base,
	"Dirname": func(path string) string {
		path = filepath.Dir(path)
		if path == "." {
			return ""
		}
		return strings.TrimRight(path, "/")
	},
}

func list(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var fl filelist
	fl.Path = r.FormValue("path")
	fl.PagePath = r.FormValue("page")
	page := r.FormValue("p")
	if page == "" {
		fl.Page = 0
	} else {
		fl.Page, _ = strconv.Atoi(page)
	}

	var err error
	fl.Files, err = GetFileList(fl.Path, fl.Page)
	if err != nil {
		fmt.Println("ListFiles:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	t, err := template.New("list").Funcs(funcMap).ParseFiles("header.html", "list.html")
	if err != nil {
		fmt.Println("ParseFiles:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	err = t.ExecuteTemplate(w, "header", fl)
	if err != nil {
		fmt.Println("Execute:", err.Error())
	}
}

func showimg(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var fl filelist
	fl.Path = r.FormValue("path")
	fl.PagePath = r.FormValue("page")
	if fl.Path == "" {
		http.Redirect(w, r, "/list", http.StatusFound)
		return
	}
	t, err := template.New("img").Funcs(funcMap).ParseFiles("header.html", "img.html")
	if err != nil {
		fmt.Println("ParseFiles:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	err = t.ExecuteTemplate(w, "header", fl)
	if err != nil {
		fmt.Println("Execute:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}
}

func showvid(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var fl filelist
	fl.Path = r.FormValue("path")
	fl.PagePath = r.FormValue("page")
	if fl.Path == "" {
		http.Redirect(w, r, "/list", http.StatusFound)
		return
	}
	t, err := template.New("video").Funcs(funcMap).ParseFiles("header.html", "video.html")
	if err != nil {
		fmt.Println("ParseFiles:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	err = t.ExecuteTemplate(w, "header", fl)
	if err != nil {
		fmt.Println("Execute:", err.Error())
	}
}

type txtfile struct {
	Path     string
	PagePath string
	Content  string
}

func showtext(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var tf txtfile
	tf.Path = r.FormValue("path")
	tf.PagePath = r.FormValue("page")
	if tf.Path == "" {
		http.Redirect(w, r, "/list", http.StatusFound)
		return
	}
	t, err := template.New("txt").Funcs(funcMap).ParseFiles("header.html", "txt.html")
	if err != nil {
		fmt.Println("ParseFiles:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	notify := w.(http.CloseNotifier).CloseNotify()
	out, err := Download(tf.Path, 0, 0, notify)
	if err != nil {
		fmt.Println("Read file:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	var body []byte
	for {
		date, ok := <-out
		if !ok {
			break
		}
		body = append(body, date...)
	}
	code := r.FormValue("encode")
	if code == "gbk" {
		body, _ = ioutil.ReadAll(transform.NewReader(bytes.NewReader(body), simplifiedchinese.GBK.NewDecoder()))
	}

	tf.Content = string(body)
	err = t.ExecuteTemplate(w, "header", tf)
	if err != nil {
		fmt.Println("Execute:", err.Error())
	}
}

func del(w http.ResponseWriter, r *http.Request) {
	from, _ := r.Cookie("from")
	r.ParseForm()
	var fl filelist
	fl.Path = r.FormValue("path")
	fl.PagePath = r.FormValue("page")
	if fl.Path == "" {
		http.Redirect(w, r, "/list", http.StatusFound)
		return
	}
	confirm := r.FormValue("confirm")
	if confirm == "yes" {
		err := Delete(fl.Path)
		if err != nil {
			fmt.Println("Delete:", err.Error())
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		http.Redirect(w, r, from.Value, http.StatusFound)
		return
	}
	t, err := template.New("delete").Funcs(funcMap).ParseFiles("header.html", "delete.html")
	if err != nil {
		fmt.Println("ParseFiles:", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	err = t.ExecuteTemplate(w, "header", fl)
	if err != nil {
		fmt.Println("Execute:", err.Error())
	}
}

func upload(w http.ResponseWriter, r *http.Request) {
	from, _ := r.Cookie("from")
	r.ParseForm()
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	path := r.FormValue("path")
	r.ParseMultipartForm(32 << 20)
	file, handler, err := r.FormFile("file")
	if err != nil {
		fmt.Println("FormFile: ", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer file.Close()
	if err := Save(filepath.Join(path, handler.Filename), file); err != nil {
		fmt.Println("save file: ", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, from.Value, http.StatusFound)
}
