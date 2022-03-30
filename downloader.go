package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
)

const (
	DefaultConcurrency = 80
	DefaultPartSize    = (4024 * 1024)
)

var (
	AccumulatedTime float64
	PartFinished    float64
	AverageTime     float64
	TotalParts      float64
	ChParts         chan int
	ChSlots         chan int
)

type Config struct {
	Url         string
	Concurrency int
	PartSize    int

	// output filename
	OutFilename    string
	CopyBufferSize int

	// is in resume mode?
	Resume bool
}

// returns filename and it's extention
func getFilenameAndExt(fileName string) (string, string) {
	ext := filepath.Ext(fileName)
	return strings.TrimSuffix(fileName, ext), ext
}

type downloader struct {
	// true if the download has been paused
	Paused bool
	config *Config

	// use to pause the download gracefully
	context context.Context
	cancel  context.CancelFunc

	bar *progressbar.ProgressBar
}

func (d *downloader) Pause() {
	d.Paused = true
	d.cancel()
}

func (d *downloader) Resume() {
	d.config.Resume = true
	d.Paused = false
	d.Download()
}

// Returns the progress bar's state
func (d *downloader) ProgressState() progressbar.State {
	if d.bar != nil {
		return d.bar.State()
	}

	return progressbar.State{}
}

// Add a number to the filename if file already exist
// For instance, if filename `hello.pdf` already exist
// it returns hello(1).pdf
func (d *downloader) renameFilenameIfNecessary() {
	if d.config.Resume {
		return // in resume mode, no need to rename
	}

	if _, err := os.Stat(d.config.OutFilename); err == nil {
		counter := 1
		filename, ext := getFilenameAndExt(d.config.OutFilename)
		outDir := filepath.Dir(d.config.OutFilename)

		for err == nil {
			log.Printf("File %s%s already exist", filename, ext)
			newFilename := fmt.Sprintf("%s(%d)%s", filename, counter, ext)
			d.config.OutFilename = path.Join(outDir, newFilename)
			_, err = os.Stat(d.config.OutFilename)
			counter += 1
		}
	}
}

func New(url string) (*downloader, error) {
	if url == "" {
		return nil, errors.New("Url is empty")
	}

	config := &Config{
		Url:         url,
		Concurrency: DefaultConcurrency,
	}

	return NewFromConfig(config)
}

func NewFromConfig(config *Config) (*downloader, error) {
	if config.Url == "" {
		return nil, errors.New("Url is empty")
	}
	if config.Concurrency < 1 {
		config.Concurrency = DefaultConcurrency
		log.Printf("Concurrency level: %d", config.Concurrency)
	}
	if config.PartSize < 1024 {
		config.PartSize = DefaultPartSize
		log.Printf("Part Size : %d", config.PartSize)
	}
	if config.OutFilename == "" {
		config.OutFilename = detectFilename(config.Url)
	}
	if config.CopyBufferSize == 0 {
		config.CopyBufferSize = 1024
	}

	d := &downloader{config: config}

	// rename file if such file already exist
	d.renameFilenameIfNecessary()
	log.Printf("Output file: %s", filepath.Base(config.OutFilename))
	return d, nil
}

func (d *downloader) getPartFilename(partNum int) string {
	return d.config.OutFilename + ".part" + strconv.Itoa(partNum)
}

func (d *downloader) Download() {
	ctx, cancel := context.WithCancel(context.Background())
	d.context = ctx
	d.cancel = cancel

	res, err := http.Head(d.config.Url)
	if err != nil {
		log.Fatal(err)
	}

	if res.StatusCode == http.StatusOK && res.Header.Get("Accept-Ranges") == "bytes" {
		contentSize, err := strconv.Atoi(res.Header.Get("Content-Length"))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("multi download\n")
		d.multiDownload(contentSize)
	} else {
		fmt.Printf("simple download\n")
		d.simpleDownload()
	}
}

// Server does not support partial download for this file
func (d *downloader) simpleDownload() {
	if d.config.Resume {
		log.Fatal("Cannot resume. Must be downloaded again")
	}

	// make a request
	res, err := http.Get(d.config.Url)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	// create the output file
	f, err := os.OpenFile(d.config.OutFilename, os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	d.bar = progressbar.DefaultBytes(int64(res.ContentLength), "downloading")

	// copy to output file
	buffer := make([]byte, d.config.CopyBufferSize)
	_, err = io.CopyBuffer(io.MultiWriter(f, d.bar), res.Body, buffer)
	if err != nil {
		log.Fatal(err)
	}
}

// download concurrently
func (d *downloader) multiDownload(contentSize int) {

	partSize := d.config.PartSize
	TotalParts = math.Ceil(float64(contentSize) / float64(partSize))

	wg := &sync.WaitGroup{}
	wg.Add(d.config.Concurrency)

	d.bar = progressbar.DefaultBytes(int64(contentSize), "downloading")

	ChParts := make(chan int, int(TotalParts))
	go func() {
		for i := 1; i <= int(TotalParts); i++ {
			fmt.Printf("writing %d to Channel \n", i)
			ChParts <- i
		}
	}()
	time.Sleep(100 * time.Millisecond)

	startRange := 0
	ChSlots := make(chan int, d.config.Concurrency)

	for PartFinished < TotalParts {
		var i int
		select {
		case i = <-ChParts:
			fmt.Printf("i=%d\n", i)
		case <-time.After(1 * time.Second):
			fmt.Printf("timeout 1, finished %.1f, total %.1f", PartFinished, TotalParts)
			continue
		}

		//this will block if ChSlots is full
		ChSlots <- 1
		fmt.Printf("downloading part %d\n", i)
		if i == int(TotalParts) {
			go d.downloadPartial(startRange, contentSize, i, wg)
		} else {
			go d.downloadPartial(startRange, startRange+partSize, i, wg)
		}
		startRange += partSize + 1
	}

	wg.Wait()
	if !d.Paused {
		d.merge()
	}
}

func (d *downloader) merge() {
	destination, err := os.OpenFile(d.config.OutFilename, os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal(err)
	}
	defer destination.Close()

	for i := 1; i <= d.config.Concurrency; i++ {
		filename := d.getPartFilename(i)
		source, err := os.OpenFile(filename, os.O_RDONLY, 0666)
		if err != nil {
			log.Fatal(err)
		}
		io.Copy(destination, source)
		source.Close()
		os.Remove(filename)
	}
}

func (d *downloader) downloadPartial(rangeStart, rangeStop int, partialNum int, wg *sync.WaitGroup) {
	defer wg.Done()
	if rangeStart >= rangeStop {
		// nothing to download
		return
	}

	fmt.Printf("pn=%d %d:%d\n", partialNum, rangeStart, rangeStop)
	// handle resume
	downloaded := 0
	if d.config.Resume {
		filePath := d.getPartFilename(partialNum)
		f, err := os.Open(filePath)
		if err == nil {
			fileInfo, err := f.Stat()
			if err == nil {
				downloaded = int(fileInfo.Size())
				// update progress bar
				d.bar.Add64(int64(downloaded))
			}
		}
		f.Close()
	}
	rangeStart += downloaded

	// create a request
	req, err := http.NewRequest("GET", d.config.Url, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, rangeStop))

	// make a request
	httpclient := http.Client{
		Timeout: 5 * time.Second,
	}

	res, err := httpclient.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	// create the output file
	outputPath := d.getPartFilename(partialNum)
	flags := os.O_CREATE | os.O_WRONLY
	if d.config.Resume {
		flags = flags | os.O_APPEND
	}
	f, err := os.OpenFile(outputPath, flags, 0666)
	if err != nil {
		log.Fatal(err)
	}

	startTime := time.Now()

	fmt.Printf("start copying file\n")
	// copy to output file
	done := false
	for !done {
		select {
		case <-d.context.Done():
			return
		default:
			_, err = io.CopyN(io.MultiWriter(f, d.bar), res.Body, int64(d.config.CopyBufferSize))
			if err != nil {
				if err == io.EOF {
					fmt.Printf("%d finished\n", partialNum)
					done = true
					break
				} else if e, ok := err.(net.Error); ok && e.Timeout() { // handle timeout
					break
				} else {
					log.Fatal(err)
				}
			}
		}

		// check if the download is taking too long
		if PartFinished/TotalParts > 0.8 && time.Since(startTime).Seconds() > 20 {
			if time.Since(startTime).Seconds() > (AverageTime * 2) {
				//return to restart this download
				ChParts <- partialNum
				d.config.Resume = true
			}
		}
	}

	res.Body.Close()
	f.Close()
	reportTime(partialNum, time.Since(startTime))

}

func reportTime(partialNum int, elapsed time.Duration) {
	x := <-ChSlots
	fmt.Printf("Chslot %d\n", x)
	AccumulatedTime += elapsed.Seconds()
	PartFinished += 1.0
	AverageTime = AccumulatedTime / PartFinished
	fmt.Printf("part %d (%.1f/%.1f) finished. Average time is %.1f seconds. \n", partialNum, PartFinished, TotalParts, AverageTime)
}

func detectFilename(url string) string {
	filename := path.Base(url)

	// remove query parameters if there exist any
	index := strings.IndexRune(filename, '?')
	if index != -1 {
		filename = filename[:index]
	}

	return filename
}
