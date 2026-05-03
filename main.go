package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

type Result struct {
	URL      string
	Protocol string
	Status   bool
	Latency  time.Duration
}

var (
	filePath   string
	numWorkers int
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "netmon",
		Short: "Simple Go network monitor by ScreamingParrot",
		Long:  `A tool for multi-threaded resource availability monitoring via HTTP and TCP protocols`,
		Run:   runMonitor,
	}

	rootCmd.Flags().StringVarP(&filePath, "file", "f", "urls.txt", "Path to file with URL list")
	rootCmd.Flags().IntVarP(&numWorkers, "workers", "w", 5, "Number of workers")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runMonitor(cmd *cobra.Command, args []string) {
	fmt.Printf("Launchig monitoring (threads: %d, file: %s)\n", numWorkers, filePath)

	db, err := initDB()
	if err != nil {
		log.Fatal("Data Base error:", err)
	}
	defer db.Close()

	urls, err := loadURLs(filePath)
	if err != nil {
		log.Fatalf("Error reading the file %s: %v", filePath, err)
	}

	jobs := make(chan string, len(urls))
	results := make(chan Result, len(urls))
	var wg sync.WaitGroup

	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for url := range jobs {
				results <- checkResource(url)
			}
		}(w)
	}

	for _, url := range urls {
		jobs <- url
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Println("\nResults:")
	fmt.Println("------------------------------------------------------------")
	for res := range results {
		statusStr := "OK"
		if !res.Status {
			statusStr = "FAIL"
		}
		fmt.Printf("[% -4s] [%-4s] %-40s | %v\n", statusStr, res.Protocol, res.URL, res.Latency.Truncate(time.Millisecond))
		saveResult(db, res)
	}
}

func initDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", "./monitor.db")
	if err != nil {
		return nil, err
	}

	query := `
    CREATE TABLE IF NOT EXISTS checks (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        url TEXT,
		protocol TEXT,
        status TEXT,
        latency_ms INTEGER,
        checked_at DATETIME
    );`

	_, err = db.Exec(query)
	return db, err
}

func saveResult(db *sql.DB, res Result) {
	query := `INSERT INTO checks (url, protocol, status, latency_ms, checked_at) VALUES (?, ?, ?, ?, ?)`
	statusStr := " OK "
	if !res.Status {
		statusStr = "FAIL"
	}

	_, err := db.Exec(query, res.URL, res.Protocol, statusStr, res.Latency.Milliseconds(), time.Now())
	if err != nil {
		log.Printf("Data Base writing error: %v", err)
	}
}

func loadURLs(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var urls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		url := strings.TrimSpace(scanner.Text())
		if url != "" {
			urls = append(urls, url)
		}
	}
	return urls, scanner.Err()
}

func checkResource(url string) Result {
	start := time.Now()
	var status bool
	var protocol string

	if strings.HasPrefix(url, "http") {
		protocol = "HTTP"
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err == nil {
			status = (resp.StatusCode == http.StatusOK)
			resp.Body.Close()
		}
	} else {
		protocol = "TCP"
		conn, err := net.DialTimeout("tcp", url, 5*time.Second)
		if err == nil {
			status = true
			conn.Close()
		}
	}

	return Result{
		URL:      url,
		Protocol: protocol,
		Status:   status,
		Latency:  time.Since(start),
	}
}
