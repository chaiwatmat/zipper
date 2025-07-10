# Zipper

Use to generate zip file and hash result as `.sha256`

## Build

### Windows

```aiignore
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o zipper.exe zipper.go
```

### Mac

```aiignore
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o zipper zipper.go
```

### Linux

```aiignore
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o zipper zipper.go
```

## Run

```aiignore
./zipper -src dist -out app-1.0.0.zip -hash \
  -copyto \\192.168.1.100\deploy -user myuser -pass pass123 \
  -useRobocopy
```

## Dry Run

```aiignore
./zipper -src dist -out app-1.0.0.zip -hash \
  -copyto \\192.168.1.100\deploy -user myuser -pass pass123 \
  -useRobocopy -dryrun
```

## Result Files

```aiignore
app-1.0.0.zip
app-1.0.0.zip.sha256
 ```