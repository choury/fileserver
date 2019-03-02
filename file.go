package main

import (
	"io"
	"io/ioutil"
	"math"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var BasePath string

type File struct {
	Path  string
	Mtime time.Time
	Size  int64
	Isdir bool
}

func (file File) Basename() string {
	return filepath.Base(file.Path)
}

var (
	imageExt = []string{".jpg", ".gif", ".png"}
	textExt  = []string{".txt"}
	videoExt = []string{".mkv", ".mp4", ".mov"}
)

func (file File) isExt(Exts []string) bool {
	ext := filepath.Ext(file.Path)
	for _, e := range Exts {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}

func (file File) Isimg() bool {
	return file.isExt(imageExt)
}

func (file File) Istxt() bool {
	return file.isExt(textExt)
}

func (file File) Isvid() bool {
	return file.isExt(videoExt)
}

func GetFileList(path string, page int) ([]File, error) {
	files, err := ioutil.ReadDir(filepath.Join(BasePath, path))
	if err != nil {
		return nil, err
	}
	fs := make([]File, 0, 10)
	for i := page * 10; i < (page+1)*10 && i < len(files); i++ {
		if f, err := Stat(filepath.Join(path, files[i].Name())); err == nil {
			fs = append(fs, f)
		} else {
			return nil, err
		}
	}
	return fs, nil
}

func Stat(path string) (File, error) {
	f, err := os.Stat(filepath.Join(BasePath, path))
	if err != nil {
		return File{}, err
	}
	return File{
		Path:  path,
		Mtime: f.ModTime(),
		Size:  f.Size(),
		Isdir: f.IsDir(),
	}, nil
}

func Download(path string, start, length int64, cancel <-chan bool) (chan []byte, error) {
	file, err := os.Open(filepath.Join(BasePath, path))
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(start, 0); err != nil {
		file.Close()
		return nil, err
	}
	if length == 0 {
		length = math.MaxInt64
	}

	out := make(chan []byte)
	go func() {
		defer file.Close()
		defer close(out)
		for length > 0 {
			select {
			case <-cancel:
				return
			default:
				data := make([]byte, 64*1024)
				count, err := file.Read(data)
				if err == io.EOF {
					return
				}
				if err != nil {
					panic(err.Error())
				}
				if count == 0 {
					return
				}
				out <- data[:count]
				length -= int64(count)
			}
		}
	}()
	return out, nil
}

func Delete(path string) error {
	return os.Remove(filepath.Join(BasePath, path))
}

func Save(path string, file multipart.File) error {
	f, err := os.OpenFile(filepath.Join(BasePath, path), os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, file)
	return err
}
