package http

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	jwt "github.com/golang-jwt/jwt"
)

func renderResponse(header http.Header, hostName string, adminName string, adminEmail string) string {

	return fmt.Sprintf(`
 	<!DOCTYPE html>
	<head>
		<title>Welcome to Border0</title>
		<style>
			body {
				background-color: #2D2D2D;
			}
			
			h1 {
				color: #C26356;
				font-size: 30px;
				font-family: Menlo, Monaco, fixed-width;
			}
			
			p {
				color: white;
				font-family: "Source Code Pro", Menlo, Monaco, fixed-width;
			}
			a {
				color: white;
				font-family: "Source Code Pro", Menlo, Monaco, fixed-width;
			  }
		</style>
	</head>
	<body>
		<h1>🚀 Welcome to the Border0 built-in webserver</h1>
		
		<p>Hi and welcome %s (%s)!<br><br>
		You're visiting the built-in Border0 webserver. This is the administrator of the Border0 Organization that started this web service: <br><i><u>%s (%s)</u></i> <br><br>

		You can now start to make your own web, ssh, or database applications available through Border0. <br><br>
		Check out the documentation for more information: <a href='https://docs.border0.com'>https://docs.border0.com</a></p>
		</p>
		<p> <br><br>
		
		Have a great day! 😊 🚀 <br><br>
		(you're visiting %s from IP %s) 
		</p>
	</body>
	</html>
	`, header["X-Auth-Name"][0], header["X-Auth-Email"][0], adminName, adminEmail, hostName, header["X-Real-Ip"][0])

}
func StartLocalHTTPServer(dir string, l net.Listener) error {
	mux := http.NewServeMux()

	if dir == "" {
		// Get Org admin info
		adminName := "Unknown"
		adminEmail := "Unknown"

		admindata, err := getAdminData()
		if err != nil {
			fmt.Println("Warning: Could not get admin data: name", err)
		} else {
			if _email, ok := admindata["user_email"].(string); ok {
				adminEmail = _email

			} else {
				fmt.Println("Warning: Could not get admin data: email")

			}
			if _name, ok := admindata["name"].(string); ok {
				adminName = _name
			} else {
				fmt.Println("Warning: Could not get admin data: name")
			}

		}

		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, renderResponse(r.Header, r.Host, adminName, adminEmail))
		})

	} else {
		fs := customFileServer(dir)
		mux.Handle("/", http.StripPrefix("/", fs))
	}

	err := http.Serve(l, mux)
	if err != nil {
		return err
	}

	return nil
}

func customFileServer(dir string) http.HandlerFunc {
	fs := http.Dir(dir)

	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if !strings.HasPrefix(path, "/") {
			path = fmt.Sprintf("/%s", path)
		}
		path = filepath.Clean(path)

		// Treat an empty path as a reference to the root directory.
		if path == "." {
			path = "/"
		}

		f, err := fs.Open(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if info.IsDir() {
			items, err := f.Readdir(-1)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			sortQuery := r.URL.Query().Get("sort")
			if sortQuery != "" {
				sortDir := r.URL.Query().Get("dir")
				if sortDir == "" {
					// Default to ascending if no direction is specified
					sortDir = "asc"
				}

				sort.Slice(items, func(i, j int) bool {
					switch sortQuery {
					case "name":
						if sortDir == "asc" {
							return items[i].Name() < items[j].Name()
						} else {
							return items[i].Name() > items[j].Name()
						}
					case "size":
						if sortDir == "asc" {
							return items[i].Size() < items[j].Size()
						} else {
							return items[i].Size() > items[j].Size()
						}
					case "modified":
						if sortDir == "asc" {
							return items[i].ModTime().Before(items[j].ModTime())
						} else {
							return items[i].ModTime().After(items[j].ModTime())
						}
					case "type":
						if sortDir == "asc" {
							return items[i].IsDir() && !items[j].IsDir()
						} else {
							return items[j].IsDir() && !items[i].IsDir()
						}
					default:
						// Sort by name by default if sortQuery is not recognized
						if sortDir == "asc" {
							return items[i].Name() < items[j].Name()
						} else {
							return items[i].Name() > items[j].Name()
						}
					}
				})
			}

			sort := r.URL.Query().Get("sort")
			dir := r.URL.Query().Get("dir")
			lastSort := r.URL.Query().Get("last_sort")
			lastDir := r.URL.Query().Get("last_dir")

			const htmlHeader = `<html><head>
			<title>Border0 File Server</title>
			<link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.3.1/css/bootstrap.min.css">
			<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/5.15.3/css/all.min.css">
			<style>
			body {
				background-color: black;
				color: #FFFFFF;
				padding: 1em;
			}
			
			h1, a, th {
				color: #FFFFFF;
			}
			.container {
				text-align: center;
				padding-top: 2em;
			}
			.b0-logo{
				max-height:50px;
				display:block;
				margin:auto;
			}
			.table-dark {
				border-top: 4px solid #FFA500;
				border-bottom: 4px solid #FFA500;
				border: 1px solid #FFFFFF;
			}
			.table-dark th {
				color: #FFFFFF;
			}
			.table-dark td {
				color: #FFFFFF;
			}
			.table-dark th a {
				color: #FFFFFF;
			}
			.table-dark td a {
				color: #FFFFFF;
			}
			</style>
			</head><body><div class="container">
			<h1 class="my-4">Directory Browser</h1>
			
			<table class="table table-striped table-dark"><thead>
			<tr>
			`
			fmt.Fprint(w, htmlHeader)

			fmt.Fprintf(w, `<th><a href="?sort=type&dir=%s&last_sort=type&last_dir=%s">Type %s</a></th>`,
				nextDir("type", sort, dir, lastSort, lastDir), dir, iconForSort("type", sort, dir))
			fmt.Fprintf(w, `<th><a href="?sort=name&dir=%s&last_sort=name&last_dir=%s">Name %s</a></th>`,
				nextDir("name", sort, dir, lastSort, lastDir), dir, iconForSort("name", sort, dir))
			fmt.Fprintf(w, `<th><a href="?sort=size&dir=%s&last_sort=size&last_dir=%s">Size %s</a></th>`,
				nextDir("size", sort, dir, lastSort, lastDir), dir, iconForSort("size", sort, dir))
			fmt.Fprintf(w, `<th><a href="?sort=modified&dir=%s&last_sort=modified&last_dir=%s">Last Modified %s</a></th>`,
				nextDir("modified", sort, dir, lastSort, lastDir), dir, iconForSort("modified", sort, dir))

			fmt.Fprint(w, `</tr></thead>
			<tbody>`)

			writeTableBody(w, items, path)
			fmt.Fprint(w, `</tbody></table>
			<p class="b0-message">
			<img class="b0-logo" src="https://download.border0.com/static/border0_logos/full-white.png" >
			</p>
			</div></body></html>`)
		} else {
			http.ServeContent(w, r, info.Name(), info.ModTime(), f)
		}
	}
}
func byteCountSI(b int64) string {
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
		TB = 1 << 40
	)
	switch {
	case b < KB:
		return fmt.Sprintf("%d B", b)
	case b < MB:
		return fmt.Sprintf("%.2f KB", float64(b)/KB)
	case b < GB:
		return fmt.Sprintf("%.2f MB", float64(b)/MB)
	case b < TB:
		return fmt.Sprintf("%.2f GB", float64(b)/GB)
	default:
		return fmt.Sprintf("%.2f TB", float64(b)/TB)
	}
}

func nextDir(sortKey, sort, dir, lastSort, lastDir string) string {
	if sortKey != sort {
		return "asc"
	}
	if dir == "asc" {
		return "desc"
	}
	return "asc"
}

func iconForSort(sortKey, sort, dir string) string {
	switch sortKey {
	case "name", "size", "modified", "type":
		if sortKey == sort {
			if dir == "asc" {
				return `<i class="fas fa-arrow-up"></i>`
			}
			return `<i class="fas fa-arrow-down"></i>`
		}
	}
	return ""
}

func writeTableBody(w http.ResponseWriter, items []os.FileInfo, path string) {
	if path != "/" {
		fmt.Fprint(w, `<tr><td><a href=".."><i class="fas fa-level-up-alt"></i></a></td><td colspan="3"><a href="..">..</a></td></tr>`)
	}

	for _, item := range items {
		icon := "<i class=\"fas fa-file\"></i>"
		if item.IsDir() {
			icon = "<i class=\"fas fa-folder\"></i>"
		}
		itemPath := filepath.Join(path, item.Name())
		if item.IsDir() {
			itemPath += "/"
		}
		size := byteCountSI(item.Size())
		modified := item.ModTime().Format("02 Jan 2006 15:04 MST")

		fmt.Fprintf(w, `<tr><td>%s</td><td><a href="%s">%s</a></td><td>%s</td><td>%s</td></tr>`,
			icon, itemPath, item.Name(), size, modified)
	}
}

func getAdminData() (jwt.MapClaims, error) {
	admintoken, err := GetToken()
	if err != nil {
		return nil, err
	}
	token, err := jwt.Parse(admintoken, nil)
	if token == nil {
		return nil, err
	}

	claims, _ := token.Claims.(jwt.MapClaims)
	return claims, nil
}

func handleTunneling(w http.ResponseWriter, r *http.Request) {
	// Modify the host if necessary.
	//r.Host = mapHost(r.Host)
	if r.Host == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	destConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		log.Printf("Failed to connect to host %s: %v", r.Host, err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	log.Printf("Tunneling from %s to %s", r.RemoteAddr, r.Host)
	w.WriteHeader(http.StatusOK)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}
	go connect(destConn, clientConn)
}

func handleHTTP(w http.ResponseWriter, req *http.Request) {
	//req.Host = mapHost(req.Host)
	if req.Host == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	req.URL.Host = req.Host
	req.URL.Scheme = "http" // You can also dynamically set the scheme based on the original request

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func connect(destConn, clientConn net.Conn) {
	defer destConn.Close()
	defer clientConn.Close()
	go copy(destConn, clientConn)
	copy(clientConn, destConn)
}

func copy(dst io.Writer, src io.Reader) {
	if _, err := io.Copy(dst, src); err != nil {
		log.Printf("something went wrong: %v", err)
	}
}

func StartHttpProxy(listener net.Listener) error {
	nl, err := net.Listen("tcp", ":8082")
	if err != nil {
		return fmt.Errorf("failed to listen for TCP: %v", err)
	}
	server := &http.Server{
		//Addr: ":8080",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("Received request %s %s %s\n", r.Method, r.Host, r.RemoteAddr)
			if r.Method == http.MethodConnect {
				handleTunneling(w, r)
			} else {
				handleHTTP(w, r)
			}
		}),
	}
	go server.Serve(nl)

	// here we accept connections over the tls socket and for each of them
	// dial and proxy to the other local listener
	for {
		rconn, err := listener.Accept()
		if err != nil {
			fmt.Printf("failed to accept new conn: %v", err)
			continue
		}

		go func() {
			defer rconn.Close()

			lconn, err := net.DialTimeout("tcp", "127.0.0.1:8082", 5*time.Second)
			if err != nil {
				fmt.Printf("failed to dial to 8082: %v", err)
				return
			}
			defer lconn.Close()

			done := make(chan struct{}, 1)
			defer close(done)

			go func() {
				if _, err := io.Copy(rconn, lconn); err != nil {
					fmt.Printf("failed to iocopy rconn -> lconn: %v", err)
					if errors.Is(err, io.EOF) {
						done <- struct{}{}
						return
					}
				}
			}()
			go func() {
				if _, err := io.Copy(lconn, rconn); err != nil {
					fmt.Printf("failed to iocopy lconn -> rconn: %v", err)
					if errors.Is(err, io.EOF) {
						done <- struct{}{}
						return
					}
				}
			}()

			<-done
		}()
	}
}
