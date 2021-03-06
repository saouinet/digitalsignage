package main

import (
	"bytes"
	"code.google.com/p/graphics-go/graphics"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/boltdb/bolt"
	"github.com/elazarl/go-bindata-assetfs"
	"html/template"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type SelectableFile struct {
	Name     string
	Selected bool
}

var address = flag.String("address", "0.0.0.0", "server address")
var port = flag.String("port", "4000", "server port")
var cache = flag.String("cache", "cache", "folder for resized images")

var folder = flag.String("images", "images", "folder with images")
var files []SelectableFile

var cancelSleep chan bool

// Populate a list of images and if none is selected select the first image
func ListFolder() {
	files = []SelectableFile{}
	raw, _ := ioutil.ReadDir(*folder)
	noneSelected := true

	config, _ := getConfig()

	for _, f := range raw {
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" {
			name := filepath.ToSlash(filepath.Join("/", *folder, f.Name()))
			if name[:2] == "//" {
				name = name[1:]
			}
			isSelected := false
			if name == config.SelectedImage {
				isSelected = true
				noneSelected = false
			}
			files = append(files, SelectableFile{name, isSelected})
		}
	}

	if noneSelected && len(files) > 0 {
		config.SelectedImage = files[0].Name
		files[0].Selected = true
		saveConfig(config)
	}
}

// Remove cached resized files with the original filename
func removeCached(filename string) {
	ext := filepath.Ext(filename)

	matches, _ := filepath.Glob(filepath.Join(*cache, strings.TrimRight(filepath.Base(filename), ext)+"*"+ext))
	if matches != nil {
		for _, file := range matches {
			os.Remove(file)
		}
	}
}

// HTTP handler for list of images or for geting, resizing and deleting images images
func GetImages(w http.ResponseWriter, r *http.Request) {
	// returns JSON list of all images
	if r.URL.Path == "/images/" {
		ListFolder()

		jsonData, err := json.MarshalIndent(files, "", "    ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if len(files) == 0 {
			w.Write([]byte("[]"))
		} else {
			w.Write(jsonData)
		}
	} else {
		// if we've got image path we try to find it on filesystem or return error
		filename := filepath.FromSlash(r.URL.Path[1:])

		absFilename, err := filepath.Abs(filename)
		if err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		wd, _ := os.Getwd()
		if !strings.HasPrefix(absFilename, wd) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if r.Method == "GET" {
			width, err1 := strconv.Atoi(r.URL.Query().Get("width"))
			height, err2 := strconv.Atoi(r.URL.Query().Get("height"))

			var x, y int
			if x, err = strconv.Atoi(r.URL.Query().Get("x")); err != nil {
				x = -1
			}
			if y, err = strconv.Atoi(r.URL.Query().Get("y")); err != nil {
				y = -1
			}

			if err1 == nil && err2 == nil && width > 0 && height > 0 {
				ext := filepath.Ext(filename)
				resizedFilename := strings.TrimRight(filepath.Base(filename), ext)
				resizedFilename += "_" + strconv.Itoa(width)
				resizedFilename += "_" + strconv.Itoa(height)
				resizedFilename += "_" + strconv.Itoa(x)
				resizedFilename += "_" + strconv.Itoa(y)
				resizedFilename += ext

				resizedFilename = filepath.Join(*cache, resizedFilename)

				// if we don't have cached image we need to generate it
				if _, err := os.Stat(resizedFilename); os.IsNotExist(err) {
					fSrc, err := os.Open(filename)
					if err != nil {
						fmt.Printf("Error: %s\n", err.Error())
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					defer fSrc.Close()

					src, _, err := image.Decode(fSrc)
					if err != nil {
						fmt.Printf("Error: %s\n", err.Error())
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}

					// if width and height is the same as the original image we don't need scale it
					if !(src.Bounds().Max.X == width && src.Bounds().Max.Y == height) || x > -1 || y > -1 {

						var dst draw.Image
						if x > -1 || y > -1 {
							if x < 0 {
								x = 0
							}
							if y < 0 {
								y = 0
							}
							// if x and y GET parameter are set we don't scale the image, but we cut it
							dst = image.NewRGBA(image.Rect(0, 0, src.Bounds().Max.X-x, src.Bounds().Max.Y-y))
							draw.Draw(dst, dst.Bounds(), src, image.Point{x, y}, draw.Src)
						} else {
							// scale the image so it will fit into width and height
							dst = image.NewRGBA(image.Rect(0, 0, width, height))
							graphics.Thumbnail(dst, src)
						}

						fDst, err := os.Create(resizedFilename)
						if err != nil {
							fmt.Printf("Error: %s\n", err.Error())
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						}
						defer fDst.Close()

						if ext == ".png" {
							png.Encode(fDst, dst)
						} else {
							jpeg.Encode(fDst, dst, &jpeg.Options{95})
						}
						filename = resizedFilename
					}
				} else {
					// cached image exists
					filename = resizedFilename
				}
			}

			http.ServeFile(w, r, filename)

			fmt.Printf("Serving image %s\n", filename)

		} else if r.Method == "DELETE" {
			if err := os.Remove(filename); err != nil {
				fmt.Printf("Error: %s\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			removeCached(filename)

			fmt.Printf("Removed %s\n", filename)

			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// HTTP handler for selcting images
// requires POST or PUT request with SelectableFile JSON data
func SelectImage(w http.ResponseWriter, r *http.Request) {
	jsonDoc, err := ioutil.ReadAll(r.Body)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	selectedFile := SelectableFile{}
	if err := json.Unmarshal(jsonDoc, &selectedFile); err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fmt.Printf("Selected %s\n", selectedFile.Name)

	config, _ := getConfig()

	// Find if the intended file exists on the server, otherwise return bad request
	for _, file := range files {
		if file.Name == selectedFile.Name {
			config.SelectedImage = selectedFile.Name
			saveConfig(config)
			ListFolder()

			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	w.WriteHeader(http.StatusBadRequest)
}

// HTTP handler for image uploads
// it handles multipart/form-data form with file field
func UploadImage(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	defer file.Close()

	out, err := os.Create(filepath.Join(*folder, header.Filename))
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// remove cached images with same name
	removeCached(header.Filename)

	fmt.Printf("Uploaded %s\n", header.Filename)

	w.WriteHeader(http.StatusNoContent)
}

// HTTP handler for screen
// If the client expects JSON it returns JSON with currently selected image
// otherwise it returns html site
func Screen(w http.ResponseWriter, r *http.Request) {
	config, _ := getConfig()
	if r.Header.Get("Content-Type") == "application/json" {
		jsonData, err := json.MarshalIndent(SelectableFile{config.SelectedImage, true}, "", "    ")
		if err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(jsonData)
	} else {
		data, err := Asset("static/screen.html")
		if err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		t := template.New("screen")
		t, _ = t.Parse(string(data[:]))

		config, _ := getConfig()

		templateData := struct {
			Selected string
			X        string
			Y        string
		}{
			config.SelectedImage,
			r.URL.Query().Get("x"),
			r.URL.Query().Get("y"),
		}

		t.Execute(w, templateData)
	}
}

// decodes gob encoded bytes to interface
func gobDecode(data []byte, out interface{}) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	err := dec.Decode(out)
	return err
}

//encodes interface to gob decoded bytes
func gobEncode(data interface{}) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(data)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type Config struct {
	Rotate        int
	SelectedImage string
}

var db *bolt.DB

// gets config from the database
// config in the database is saved as bytes and is encoded with gob
// so after reading it is decoded with gobDecode
func getConfig() (Config, error) {
	config := &Config{}
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("data"))

		val := b.Get([]byte("config"))
		if val == nil {
			return fmt.Errorf("No config in database")
		}

		return gobDecode(val, config)
	})
	return *config, err
}

// saves config to the database
// uses gobEncode to dump config structure to byte slice
func saveConfig(config Config) error {
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("data"))

		encoded, err := gobEncode(config)
		if err != nil {
			return err
		}

		return b.Put([]byte("config"), encoded)
	})
}

// HTTP handler for geting or saving data
func ConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		config, err := getConfig()
		if err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonData, err := json.MarshalIndent(config, "", "    ")
		if err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(jsonData)
	} else {
		jsonDoc, err := ioutil.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		config := Config{}
		if err := json.Unmarshal(jsonDoc, &config); err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		oldConf, _ := getConfig()
		fmt.Println(config)
		saveConfig(config)

		// cancel rotation timeout so it will start rotating with new time
		if oldConf.Rotate > 0 {
			cancelSleep <- true
		}

		jsonData, err := json.MarshalIndent(config, "", "    ")
		if err != nil {
			fmt.Printf("Error: %s\n", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(jsonData)
	}
}

func main() {
	flag.Parse()

	// Open database (or create it) and populate it with defaults if nothing exists yet
	var err error
	db, err = bolt.Open("database.db", 0600, nil)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("data"))
		return err
	})

	var config Config
	config, err = getConfig()
	if err != nil {
		config = Config{0, ""}
		saveConfig(config)
	}

	os.Mkdir(*cache, 0755)
	os.Mkdir(*folder, 0755)
	ListFolder()

	// This function is used to rotate images if Rotate in config is set
	cancelSleep = make(chan bool)
	go func() {
	Loop:
		for {
			config, _ := getConfig()
			if config.Rotate > 0 {
				timer := make(chan bool)
				go func() {
					time.Sleep(time.Duration(config.Rotate) * time.Second)
					timer <- true
				}()

				select {
				case <-cancelSleep:
					continue Loop
				case <-timer:
				}

				for i, file := range files {
					if file.Selected {
						if i == len(files)-1 {
							config.SelectedImage = files[0].Name
						} else {
							config.SelectedImage = files[i+1].Name
						}
						saveConfig(config)
						ListFolder()
						break
					}
				}
			} else {
				time.Sleep(time.Second)
			}
		}
	}()

	http.HandleFunc("/images/", GetImages)
	http.HandleFunc("/select", SelectImage)
	http.HandleFunc("/upload", UploadImage)
	http.HandleFunc("/screen", Screen)
	http.HandleFunc("/config", ConfigHandler)

	http.Handle("/", http.FileServer(&assetfs.AssetFS{Asset: Asset, AssetDir: AssetDir, Prefix: "static"}))

	fmt.Println("Digital signage demo by Visionect")
	fmt.Println("http://www.visionect.com")
	fmt.Printf("\nStarting server at: http://%s:%s/ \n", *address, *port)
	fmt.Printf("Serving images from: %s \n", *folder)
	fmt.Println("\nHelp available with -h, exit with Ctrl-C")

	err = http.ListenAndServe(*address+":"+*port, nil)
	if err != nil {
		panic("ListenAndServe: " + err.Error())
	}
}
