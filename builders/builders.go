package builders

import (
	"bytes"
	"fmt"
	"path/filepath"
	"time"

	"github.com/influx6/assets"
	"github.com/influx6/flux"
	"github.com/influx6/gotask/fs"
	"github.com/microcosm-cc/bluemonday"
	"github.com/russross/blackfriday"
)

// GoInstaller calls `go install` from the path it receives from its data pipes
func GoInstaller() flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if path, ok := data.(string); ok {
			if err := GoDeps(path); err != nil {
				root.ReplyError(err)
			}
		}
	}))
}

// GoInstallerWith calls `go install` everysingle time to the provided path once a signal is received
func GoInstallerWith(path string) flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		if err := GoDeps(path); err != nil {
			root.ReplyError(err)
		}
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
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		if err := Gobuild(cmd.Path, cmd.Name, cmd.Args); err != nil {
			root.ReplyError(err)
		}
	}))
}

// GoArgsBuilder calls `go run` with the command it receives from its data pipes usingthe GobuildArgs function
func GoArgsBuilder() flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, data interface{}) {
		if cmd, ok := data.([]string); ok {
			if err := GobuildArgs(cmd); err != nil {
				root.ReplyError(err)
			}
		}
	}))
}

// GoArgsBuilderWith calls `go run` everysingle time to the provided path once a signal is received using the GobuildArgs function
func GoArgsBuilderWith(cmd []string) flux.Reactor {
	return flux.Reactive(flux.SimpleMuxer(func(root flux.Reactor, _ interface{}) {
		if err := GobuildArgs(cmd); err != nil {
			root.ReplyError(err)
		}
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
			channel <- true
		}

	}))
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

		root.Reply(&fs.FileWrite{Data: js, Path: filepath.Join(config.Folder, jsfile)})
		root.Reply(&fs.FileWrite{Data: jsmap, Path: filepath.Join(config.Folder, jsmapfile)})
	}))
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
			root.Reply(&RenderFile{Path: fr.Path, Data: fr.Data.Bytes()})
		}
	})
}

// FileWrite2RenderFile turns a fs.FileWrite into a RenderFile object
func FileWrite2RenderFile() flux.Reactor {
	return flux.FlatSimple(func(root flux.Reactor, data interface{}) {
		if fr, ok := data.(*fs.FileWrite); ok {
			root.Reply(&RenderFile{Path: fr.Path, Data: fr.Data.Bytes()})
		}
	})
}
