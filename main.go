package main

import (
	"github.com/wanghonggao007/gopherbot/bot"

	// NOTE: If compiling gopherbot yourself, you can comment out or remove
	// most of the import lines below to shrink the binary or remove unwanted
	// or unneeded funcationality. You'll need at least one connector for your
	// bot to be useful, however.

	// *** Included connectors

	_ "github.com/wanghonggao007/gopherbot/connectors/rocket"
	_ "github.com/wanghonggao007/gopherbot/connectors/slack"

	// NOTE: if you build with '-tags test', the terminal connector will also
	// show emitted events.
	_ "github.com/wanghonggao007/gopherbot/connectors/terminal"

	// *** Included brain implementations

	_ "github.com/wanghonggao007/gopherbot/brains/dynamodb"
	_ "github.com/wanghonggao007/gopherbot/brains/file"

	// *** Included history implementations
	_ "github.com/wanghonggao007/gopherbot/history/file"

	// Many included plugins already have 'Disabled: true', but you can also
	// disable by adding that line to conf/plugins/<plugname>.yaml

	// *** Included Elevator plugins

	_ "github.com/wanghonggao007/gopherbot/goplugins/duo"
	_ "github.com/wanghonggao007/gopherbot/goplugins/totp"

	// *** Included Authorizer plugins

	_ "github.com/wanghonggao007/gopherbot/goplugins/groups"

	// *** Included Go plugins, of varying quality

	_ "github.com/wanghonggao007/gopherbot/goplugins/help"
	_ "github.com/wanghonggao007/gopherbot/goplugins/knock"
	_ "github.com/wanghonggao007/gopherbot/goplugins/links"
	_ "github.com/wanghonggao007/gopherbot/goplugins/lists"
	_ "github.com/wanghonggao007/gopherbot/goplugins/meme"
	_ "github.com/wanghonggao007/gopherbot/goplugins/ping"

	// Helpful plugin for a Slack bot admin
	_ "github.com/wanghonggao007/gopherbot/goplugins/slackutil"

	/* Enable profiling. You can shrink the binary by removing this, but if the
	   robot ever stops responding for any reason, it's handy for getting a
	   dump of all goroutines. Example usage:

	   $ go tool pprof http://localhost:8888/debug/pprof/goroutine
	   ...
	   Entering interactive mode (type "help" for commands, "o" for options)
	   (pprof) list lnxjedi
	   Total: 11
	   ROUTINE ======================== github.com/lnxjedi/gopherbot/bot...
	*/
	_ "net/http/pprof"
)

var Version = "v2.0.0-snapshot"
var Commit = "(not set)"

func main() {
	versionInfo := bot.VersionInfo{
		Version: Version,
		Commit:  Commit,
	}
	bot.Start(versionInfo)
}
