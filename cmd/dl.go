package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	downloader "github.com/mostafa-asg/go-dl"
)

func main() {
	concurrency := flag.Int("c", downloader.DefaultConcurrency, "Concurrency level")
	partsize := flag.Int("s", downloader.DefaultPartSize, "Part size in bytes")
	filename := flag.String("o", "", "Output file name")
	bufferSize := flag.Int("buffer-size", 32*1024, "The buffer size to copy from http response body")
	resume := flag.Bool("resume", false, "Resume the download")
	verbose := flag.Bool("v", false, "verbose output")

	flag.Parse()
	args := flag.Args() //non-flag arguments
	if len(args) != 1 {
		flag.Usage()
		log.Fatal("Please specify the url. ")
	}
	url := args[0]
	fmt.Printf("url=%s\n", url)

	config := &downloader.Config{
		Url:            url,
		Concurrency:    *concurrency,
		PartSize:       *partsize,
		OutFilename:    *filename,
		CopyBufferSize: *bufferSize,
		Resume:         *resume,
		Verbose:        *verbose,
	}
	d, err := downloader.NewFromConfig(config)
	if err != nil {
		log.Fatal(err.Error())
	}

	termCh := make(chan os.Signal)
	signal.Notify(termCh, os.Interrupt)
	go func() {
		<-termCh
		println("\nExiting ...")
		d.Pause()
	}()

	d.Download()
	if d.Paused {
		println("\nDownload has paused. Resume it again with -resume=true parameter.")
	} else {
		println("Downloadd completed.")
	}
}
