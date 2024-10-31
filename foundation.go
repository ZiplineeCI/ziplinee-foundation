package foundation

import (
	"context"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"unicode"

	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
)

var (
	// seed random number
	r = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// InitGracefulShutdownHandling generates the channel that listens to SIGTERM and a waitgroup to use for finishing work when shutting down
func InitGracefulShutdownHandling() (gracefulShutdown chan os.Signal, waitGroup *sync.WaitGroup) {

	// define channel used to gracefully shutdown the application
	gracefulShutdown = make(chan os.Signal)

	signal.Notify(gracefulShutdown, syscall.SIGTERM, syscall.SIGINT)

	waitGroup = &sync.WaitGroup{}

	return gracefulShutdown, waitGroup
}

// HandleGracefulShutdown waits for SIGTERM to unblock gracefulShutdown and waits for the waitgroup to await pending work
func HandleGracefulShutdown(gracefulShutdown chan os.Signal, waitGroup *sync.WaitGroup, functionsOnShutdown ...func()) {

	signalReceived := <-gracefulShutdown
	log.Info().
		Msgf("Received signal %v. Waiting for running tasks to finish...", signalReceived)

	// execute any passed function
	for _, f := range functionsOnShutdown {
		f()
	}

	waitGroup.Wait()

	log.Info().Msg("Shutting down...")
}

// InitCancellationContext adds cancelation to a context and on sigterm triggers the cancel function
func InitCancellationContext(ctx context.Context) context.Context {

	ctx, cancel := context.WithCancel(context.Background())

	// define channel used to trigger cancellation
	cancelChannel := make(chan os.Signal)

	signal.Notify(cancelChannel, syscall.SIGTERM, syscall.SIGINT)

	go func(cancelChannel chan os.Signal, cancel context.CancelFunc) {
		<-cancelChannel
		cancel()
	}(cancelChannel, cancel)

	return ctx
}

// ApplyJitter adds +-25% jitter to the input
func ApplyJitter(input int) (output int) {

	deviation := int(0.25 * float64(input))

	return input - deviation + r.Intn(2*deviation)
}

// WatchForFileChanges waits for a change to the provided file path and then executes the function
func WatchForFileChanges(filePath string, functionOnChange func(fsnotify.Event)) {
	// copied from https://github.com/spf13/viper/blob/v1.3.1/viper.go#L282-L348
	initWG := sync.WaitGroup{}
	initWG.Add(1)
	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Fatal().Err(err).Msg("Creating file system watcher failed")
		}
		defer watcher.Close()

		// we have to watch the entire directory to pick up renames/atomic saves in a cross-platform way
		file := filepath.Clean(filePath)
		fileDir, _ := filepath.Split(file)
		realFile, _ := filepath.EvalSymlinks(filePath)

		eventsWG := sync.WaitGroup{}
		eventsWG.Add(1)
		go func() {
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok { // 'Events' channel is closed
						eventsWG.Done()
						return
					}
					currentFile, _ := filepath.EvalSymlinks(filePath)
					// we only care about the key file with the following cases:
					// 1 - if the key file was modified or created
					// 2 - if the real path to the key file changed (eg: k8s ConfigMap/Secret replacement)
					const writeOrCreateMask = fsnotify.Write | fsnotify.Create
					if (filepath.Clean(event.Name) == file &&
						event.Op&writeOrCreateMask != 0) ||
						(currentFile != "" && currentFile != realFile) {
						realFile = currentFile

						functionOnChange(event)
					} else if filepath.Clean(event.Name) == file &&
						event.Op&fsnotify.Remove&fsnotify.Remove != 0 {
						eventsWG.Done()
						return
					}

				case err, ok := <-watcher.Errors:
					if ok { // 'Errors' channel is not closed
						log.Warn().Err(err).Msg("Watcher error")
					}
					eventsWG.Done()
					return
				}
			}
		}()
		watcher.Add(fileDir)
		initWG.Done()   // done initalizing the watch in this go routine, so the parent routine can move on...
		eventsWG.Wait() // now, wait for event loop to end in this go-routine...
	}()
	initWG.Wait() // make sure that the go routine above fully ended before returning
}

// FileExists checks if a file exists
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// DirExists checks if a directory exists
func DirExists(directory string) bool {
	info, err := os.Stat(directory)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

// PathExists checks if a directory exists
func PathExists(path string) bool {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return true
}

// StringArrayContains checks if an array contains a specific value
func StringArrayContains(array []string, search string) bool {
	for _, v := range array {
		if v == search {
			return true
		}
	}
	return false
}

// IntArrayContains checks if an array contains a specific value
func IntArrayContains(array []int, search int) bool {
	for _, v := range array {
		if v == search {
			return true
		}
	}
	return false
}

// ToUpperSnakeCase turns any input string into an upper snake cased string
func ToUpperSnakeCase(in string) string {
	runes := []rune(in)
	length := len(runes)

	var out []rune
	for i := 0; i < length; i++ {
		if i > 0 && unicode.IsUpper(runes[i]) && ((i+1 < length && unicode.IsLower(runes[i+1])) || unicode.IsLower(runes[i-1])) {
			out = append(out, '_')
		}
		out = append(out, unicode.ToUpper(runes[i]))
	}

	snake := string(out)

	// make sure nothing but alphanumeric characters and underscores are returned
	reg, err := regexp.Compile("[^A-Z0-9]+")
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed converting %v to upper snake case", in)
	}
	cleanSnake := reg.ReplaceAllString(snake, "_")

	return cleanSnake
}

// ToLowerSnakeCase turns any input string into an lower snake cased string
func ToLowerSnakeCase(in string) string {
	runes := []rune(in)
	length := len(runes)

	var out []rune
	for i := 0; i < length; i++ {
		if i > 0 && unicode.IsUpper(runes[i]) && ((i+1 < length && unicode.IsLower(runes[i+1])) || unicode.IsLower(runes[i-1])) {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(runes[i]))
	}

	snake := string(out)

	// make sure nothing but alphanumeric characters and underscores are returned
	reg, err := regexp.Compile("[^a-z0-9]+")
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed converting %v to lower snake case", in)
	}
	cleanSnake := reg.ReplaceAllString(snake, "_")

	return cleanSnake
}
