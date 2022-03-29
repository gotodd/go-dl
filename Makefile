all:
	go build -ldflags='-s -w' -o dl cmd/dl.go
win:
	GOOS=windows GOARCH=amd64 go build -ldflags='-s -w' -o dl.exe cmd/dl.go
clean:
	rm -f dl dl.exe
	ls -lrt --color
