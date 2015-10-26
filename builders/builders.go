package builders

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/influx6/assets"
	"github.com/influx6/flux"
	"github.com/influx6/reactors/fs"
	"github.com/microcosm-cc/bluemonday"
	"github.com/russross/blackfriday"
)

// GoInstaller calls `go install` from the path it receives from its data pipes
func GoInstaller() flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if path, ok := data.(string); ok {
			if err := GoDeps(path); err != nil {
				root.ReplyError(err)
				return
			}
			root.Reply(true)
		}
	}))
}

// GoInstallerWith calls `go install` everysingle time to the provided path once a signal is received
func GoInstallerWith(path string) flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		if err := GoDeps(path); err != nil {
			root.ReplyError(err)
			return
		}
		root.Reply(true)
	}))
}

// GoRunner calls `go run` with the command it receives from its data pipes
func GoRunner() flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if cmd, ok := data.(string); ok {
			root.Reply(GoRun(cmd))
		}
	}))
}

// GoRunnerWith calls `go run` everysingle time to the provided path once a signal is received
func GoRunnerWith(cmd string) flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		root.Reply(GoRun(cmd))
	}))
}

// BuildConfig defines a configuration to be passed into a GoBuild/GoBuildWith Task
type BuildConfig struct {
	Path string
	Name string
	Args []string
}

func validateBuildConfig(b BuildConfig) {
	if b.Name == "" {
		panic("buildConfig.Name can not be empty,supply a name for the build")
	}

	if b.Path == "" {
		panic("buildConfig.Path can not be empty,supply a path to store the build")
	}
}

// GoBuilder calls `go run` with the command it receives from its data pipes, using the GoBuild function
func GoBuilder() flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if cmd, ok := data.(BuildConfig); ok {
			if err := Gobuild(cmd.Path, cmd.Name, cmd.Args); err != nil {
				root.ReplyError(err)
			}
		}
	}))
}

// GoBuilderWith calls `go run` everysingle time to the provided path once a signal is received using the GoBuild function
func GoBuilderWith(cmd BuildConfig) flux.Reactor {
	validateBuildConfig(cmd)
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		if err := Gobuild(cmd.Path, cmd.Name, cmd.Args); err != nil {
			root.ReplyError(err)
			return
		}
		root.Reply(true)
	}))
}

// GoArgsBuilder calls `go run` with the command it receives from its data pipes usingthe GobuildArgs function
func GoArgsBuilder() flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if cmd, ok := data.([]string); ok {
			if err := GobuildArgs(cmd); err != nil {
				root.ReplyError(err)
				return
			}
			root.Reply(true)
		}
	}))
}

// GoArgsBuilderWith calls `go run` everysingle time to the provided path once a signal is received using the GobuildArgs function
func GoArgsBuilderWith(cmd []string) flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		if err := GobuildArgs(cmd); err != nil {
			root.ReplyError(err)
			return
		}
		root.Reply(true)
	}))
}

// CommandLauncher returns a new Task generator that builds a command executor that executes a series of command every time it receives a signal, it sends out a signal onces its done running all commands
func CommandLauncher(cmd []string) flux.Reactor {
	var channel chan bool
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		if channel == nil {
			channel = RunCMD(cmd, func() {
				root.Reply(true)
			})
		}

		select {
		case <-root.CloseNotify():
			close(channel)
			return
		case <-time.After(0):
			channel <- true
		}

	}))
}

// BinaryLauncher returns a new Task generator that builds a binary runner from the given properties, which causing a relaunch of a binary file everytime it recieves a signal,  it sends out a signal onces its done running all commands
func BinaryLauncher(bin string, args []string) flux.Reactor {
	var channel chan bool

	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if channel == nil {
			channel = RunBin(bin, args, func() {
				root.Reply(true)
			}, func() {
				go root.Close()
			})
		}

		select {
		case <-root.CloseNotify():
			close(channel)
			return
		case <-time.After(0):
			//force check of boolean values to ensure we can use correct signal
			if cmd, ok := data.(bool); ok {
				channel <- cmd
				return
			}

			//TODO: should we fallback to sending true if we receive a signal normally? or remove this
			// channel <- true
		}

	}))
}

// BinaryBuildConfig defines a configuration to be passed into a BinaryBuildLuncher
type BinaryBuildConfig struct {
	Path      string
	Name      string
	BuildArgs []string //arguments to be used in building
	RunArgs   []string //arguments to be used in running
}

func validateBinaryBuildConfig(b BinaryBuildConfig) {
	if b.Name == "" {
		panic("buildConfig.Name can not be empty,supply a name for the build")
	}

	if b.Path == "" {
		panic("buildConfig.Path can not be empty,supply a path to store the build")
	}
}

// BinaryBuildLauncher combines the builder and binary runner to provide a simple and order-based process,
// the BinaryLauncher is only created to handling a binary lunching making it abit of a roundabout to time its response to wait until another process finishes, but BinaryBuildLuncher cleans out the necessity and provides a reactor that embedds the necessary call routines while still response the: Build->Run or StopRunning->Build->Run process in development
func BinaryBuildLauncher(cmd BinaryBuildConfig) flux.Reactor {
	validateBinaryBuildConfig(cmd)

	// first generate the output file name from the config
	var basename = cmd.Name

	if runtime.GOOS == "windows" {
		basename = fmt.Sprintf("%s.exe", basename)
	}

	binfile := filepath.Join(cmd.Path, basename)

	//create the root stack which connects all the sequence of build and run together
	buildStack := flux.ReactorStack()

	//package builder
	builder := GoBuilderWith(BuildConfig{Path: cmd.Path, Name: cmd.Name, Args: cmd.BuildArgs})

	//package runner
	runner := BinaryLauncher(binfile, cmd.RunArgs)

	//when buildStack receives a signal, we will send a bool(false) signal to runner to kill the current process
	buildStack.React(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		//tell runner to kill process
		// log.Printf("sending to runner")
		runner.Send(false)
		//forward the signal down the chain
		root.Reply(data)
	}), true)

	//connect the build stack first then the runn stack to force order
	buildStack.Bind(builder, true)
	buildStack.Bind(runner, true)

	return buildStack
}

// GoFileLauncher returns a new Task generator that builds a binary runner from the given properties, which causing a relaunch of a binary file everytime it recieves a signal,  it sends out a signal onces its done running all commands
func GoFileLauncher(goFile string, args []string) flux.Reactor {
	var channel chan bool

	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if channel == nil {
			channel = RunGo(goFile, args, func() {
				root.Reply(true)
			}, func() {
				go root.Close()
			})
		}

		select {
		case <-root.CloseNotify():
			close(channel)
			return
		case <-time.After(0):
			channel <- true
		}

	}))
}

// JSBuildConfig provides a configuration for JSBuildLauncher
type JSBuildConfig struct {
	Package    string
	Folder     string   //Folder represents the path to be added to the name of where to store the files
	FileName   string   // FileName is the output name for the js and js.map files
	PackageDir string   // Optional: PackageDir is an optional directory to be imported into build process
	Tags       []string //Optional: Tags are optional build tags for build process
	Verbose    bool     // Optional: verbose value for gopherjs builder
}

// JSBuildLauncher returns a Task generator that builds a new jsbuild task giving the specific configuration and on every reception of signals rebuilds and sends off a FileWrite for each file i.e the js and js.map file
func JSBuildLauncher(config JSBuildConfig) flux.Reactor {
	if config.Package == "" {
		panic("JSBuildConfig.Package can not be empty")
	}

	if config.FileName == "" {
		config.FileName = "jsapp.build"
	}

	var session *JSSession
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if session == nil {
			session = NewJSSession(config.Tags, config.Verbose, false)
		}

		//do we have an optional PackageDir that is not empty ? if so we use session.BuildDir
		//else session.BuildPkg
		var js, jsmap *bytes.Buffer
		var err error

		if config.PackageDir != "" {
			js, jsmap, err = session.BuildDir(config.PackageDir, config.Package, config.FileName)
		} else {
			js, jsmap, err = session.BuildPkg(config.Package, config.FileName)
		}

		if err != nil {
			root.ReplyError(err)
			return
		}

		jsfile := fmt.Sprintf("%s.js", config.FileName)
		jsmapfile := fmt.Sprintf("%s.js.map", config.FileName)

		root.Reply(&fs.FileWrite{Data: js.Bytes(), Path: filepath.Join(config.Folder, jsfile)})
		root.Reply(&fs.FileWrite{Data: jsmap.Bytes(), Path: filepath.Join(config.Folder, jsmapfile)})
	}))
}

// JSLauncher returns a reactor that on receiving a signal builds the gopherjs package as giving in the config and writes it out using a FileWriter
func JSLauncher(config JSBuildConfig) flux.Reactor {
	stack := flux.ReactStack(JSBuildLauncher(config))
	stack.Bind(fs.FileWriter(nil), true)
	return stack
}

// PackageWatcher generates a fs.Watch tasker which given a valid package name will retrieve the package directory and
// those of its dependencies and watch it for changes, you can supply a validator function to filter out what path you
// prefer to watch or not to
func PackageWatcher(packageName string, vx assets.PathValidator) (flux.Reactor, error) {
	pkg, err := assets.GetPackageLists(packageName)
	if err != nil {
		return nil, err
	}

	return fs.WatchSet(fs.WatchSetConfig{
		Path:      pkg,
		Validator: vx,
	}), nil
}

// RenderFile repesents a render requested used by ByteRender for handling rendering
type RenderFile struct {
	Path string
	Data []byte
}

// ErrNotRenderFile is returned when a type is not a *RenderFile
var ErrNotRenderFile = errors.New("Value Is Not a *RenderFile")

// RenderMux defines a rendering function which takes what value it gets and spews a modded version
type RenderMux func([]byte) []byte

// ByteRenderer provides a baseline worker for building rendering tasks eg markdown. It expects to receive a *RenderFile and then it returns another *RenderFile containing the outputed rendered data with the path from the previous RenderFile,this allows chaining with other ByteRenderers
func ByteRenderer(fx RenderMux) flux.Reactor {
	if fx == nil {
		panic("RenderMux cant be nil for ByteRender")
	}
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if databytes, ok := data.(*RenderFile); ok {
			root.Reply(&RenderFile{Path: databytes.Path, Data: fx(databytes.Data)})
		}
	}))
}

// BlackFriday returns a reactor which expects a RenderFile whoes data gets converted into markdown and returns a RenderedFile as output signal, it builds ontop of ByteRenderer
func BlackFriday() flux.Reactor {
	return ByteRenderer(blackfriday.MarkdownCommon)
}

// BlueMonday builts ontop of ByteRenderer by using BlueMonday as rendering, using the UGCPolicy
func BlueMonday() flux.Reactor {
	return ByteRenderer(bluemonday.UGCPolicy().SanitizeBytes)
}

// BlackMonday combines a BlackFriday and BlueMonday to create a more Sanitized markdown output
func BlackMonday() flux.Reactor {
	return flux.LiftOut(true, BlackFriday(), BlueMonday())
}

// FileRead2RenderFile turns a fs.FileRead into a RenderFile object
func FileRead2RenderFile() flux.Reactor {
	return flux.FlatSimple(func(root flux.Reactor, data interface{}) {
		if fr, ok := data.(*fs.FileRead); ok {
			root.Reply(&RenderFile{Path: fr.Path, Data: fr.Data})
		}
	})
}

// FileWrite2RenderFile turns a fs.FileWrite into a RenderFile object
func FileWrite2RenderFile() flux.Reactor {
	return flux.FlatSimple(func(root flux.Reactor, data interface{}) {
		if fr, ok := data.(*fs.FileWrite); ok {
			root.Reply(&RenderFile{Path: fr.Path, Data: fr.Data})
		}
	})
}

// RenderFile2FileWrite turns a RenderFile into a fs.FileWrite object
func RenderFile2FileWrite() flux.Reactor {
	return flux.FlatSimple(func(root flux.Reactor, data interface{}) {
		if fr, ok := data.(*RenderFile); ok {
			root.Reply(&fs.FileWrite{Path: fr.Path, Data: fr.Data})
		}
	})
}

// FileWriteMutator provides a function type that mutates and returns a fs.FileWrite object
type FileWriteMutator func(w *fs.FileWrite) *fs.FileWrite

var defaultMutateFileWrite = func(w *fs.FileWrite) *fs.FileWrite { return w }

// MutateFileWrite turns a fs.FileWrite into a RenderFile object
func MutateFileWrite(fx FileWriteMutator) flux.Reactor {
	if fx == nil {
		fx = defaultMutateFileWrite
	}
	return flux.FlatSimple(func(root flux.Reactor, data interface{}) {
		if fr, ok := data.(*fs.FileWrite); ok {
			root.Reply(fx(fr))
		}
	})
}

// MarkConfig provides a config for turning inputs from file through a markdown preprocessor then
// save this files with an extension change into the given folder
type MarkConfig struct {
	SaveDir     string                          // optional: path to save output files into but if empty,it uses the files own path original path
	Ext         string                          //Optional: supply it incase you wish to change the file extension, else use a .md extension
	Sanitize    bool                            //Optional: if true will combine markdown and bluemonday together
	PathMux     func(MarkConfig, string) string //Optional: if present will be used to generate the file path which gets its extension swapped and is used as the output filepath
	BeforeWrite FileWriteMutator
}

// MarkFriday combines a fs.FilReader with a markdown processor which then pipes into a fs.FileWriter to save the output
func MarkFriday(m MarkConfig) flux.Reactor {
	if m.Ext == "" {
		m.Ext = ".md"
	}

	var markdown flux.Reactor

	if m.Sanitize {
		markdown = BlackMonday()
	} else {
		markdown = BlackFriday()
	}

	reader := fs.FileReader()

	// reader.React(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
	// 	log.Printf("reader %s", data)
	// }), true)

	writer := fs.FileWriter(func(path string) string {
		var dir string

		if m.PathMux != nil {
			dir = m.PathMux(m, path)
		} else {
			//get the current directory of the path
			cdir := filepath.Dir(path)

			//if we have a preset folder replace it
			if m.SaveDir != "" {
				cdir = m.SaveDir
			}

			// strip out the directory from the path and only use the base name
			base := filepath.Base(path)

			//combine with the dir for the final path
			dir = filepath.Join(cdir, base)
		}

		//grab our own extension
		ext := strings.Replace(m.Ext, ".", "", -1)

		//strip off the extension and add ours
		return strings.Replace(dir, filepath.Ext(dir), fmt.Sprintf(".%s", ext), -1)
	})

	stack := flux.ReactStack(reader)
	stack.Bind(FileRead2RenderFile(), true)
	stack.Bind(markdown, true)
	stack.Bind(RenderFile2FileWrite(), true)
	stack.Bind(MutateFileWrite(m.BeforeWrite), true)
	stack.Bind(writer, true)

	return stack
}

// MarkStreamConfig defines the configuration to be recieved by MarkFridayStream for auto-streaming markdown files
type MarkStreamConfig struct {
	InputDir    string
	SaveDir     string
	Ext         string
	Sanitize    bool
	Validator   assets.PathValidator
	Mux         assets.PathMux
	BeforeWrite FileWriteMutator
}

// MarkFridayStream returns a flux.Reactor that takes the given config and generates a markdown auto-converter, when
// it recieves any signals,it will stream down each file and convert the markdown input and save into the desired output path
func MarkFridayStream(m MarkStreamConfig) (flux.Reactor, error) {
	streamer, err := fs.StreamListings(fs.ListingConfig{
		Path:      m.InputDir,
		Validator: m.Validator,
		Mux:       m.Mux,
	})

	if err != nil {
		return nil, err
	}

	absPath, _ := filepath.Abs(m.InputDir)

	markdown := MarkFriday(MarkConfig{
		SaveDir:     m.SaveDir,
		Ext:         m.Ext,
		Sanitize:    m.Sanitize,
		BeforeWrite: m.BeforeWrite,
		PathMux: func(m MarkConfig, path string) string {
			//we find the index of the absolute path we need to index
			index := strings.Index(path, absPath)

			// log.Printf("absolute: indexing path %s with %s -> %d", path, absPath, index)

			//if we found one then strip the absolute path and combine with SaveDir
			if index != -1 {
				return filepath.Join(m.SaveDir, strings.Replace(path, absPath, "./", 1))
			}

			//we didnt find one so we find the base, backtrack a step,strip that off and combine with the SaveDir
			base := filepath.Join(filepath.Base(path), "..")
			index = strings.Index(path, base)

			// log.Printf("fallback: indexing path %s with %s -> %d", path, base, index)

			return filepath.Join(m.SaveDir, strings.Replace(path, base, "./", 1))
		},
	})

	stack := flux.ReactStack(streamer)
	stack.Bind(markdown, true)

	return stack, nil
}

// GoFridayStream combines the MarkFridayStream auto-coverter to create go template ready files from the output of processing markdown files
func GoFridayStream(m MarkStreamConfig) (flux.Reactor, error) {
	if (&m).Ext == "" {
		(&m).Ext = ".tmpl"
	}

	(&m).BeforeWrite = func(w *fs.FileWrite) *fs.FileWrite {
		base := filepath.Base(w.Path)
		ext := filepath.Ext(base)
		base = strings.Replace(base, ext, "", -1)
		mod := append([]byte(fmt.Sprintf(`{{define "%s"}}

`, base)), w.Data...)
		mod = append(mod, []byte("\n{{ end }}\n")...)
		w.Data = mod
		return w
	}
	return MarkFridayStream(m)
}
