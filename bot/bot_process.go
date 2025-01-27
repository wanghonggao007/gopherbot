// Package bot provides the internal machinery for most of Gopherbot.
package bot

/* bot.go defines core data structures and public methods for startup.
   handler.go has the methods for callbacks from the connector, */

import (
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"
)

// VersionInfo holds information about the version, duh. (stupid linter)
type VersionInfo struct {
	Version, Commit string
}

// configPath is optional, installPath is where gopherbot(.exe) is
var configPath, installPath string

var botVersion VersionInfo

var random *rand.Rand

var connectors = make(map[string]func(Handler, *log.Logger) Connector)

// RegisterConnector should be called in an init function to register a type
// of connector. Currently only Slack is implemented.
func RegisterConnector(name string, connstarter func(Handler, *log.Logger) Connector) {
	if stopRegistrations {
		return
	}
	if connectors[name] != nil {
		log.Fatal("Attempted registration of duplicate connector:", name)
	}
	connectors[name] = connstarter
}

// robot holds all the interal data relevant to the Bot. Most of it is populated
// by loadConfig, other stuff is populated by the connector.
var botCfg struct {
	Connector                            // Connector interface, implemented by each specific protocol
	adminUsers           []string        // List of users with access to administrative commands
	alias                rune            // single-char alias for addressing the bot
	botinfo              UserInfo        // robot's name, ID, email, etc.
	adminContact         string          // who to contact for problems with the bot
	mailConf             botMailer       // configuration to use when sending email
	ignoreUsers          []string        // list of users to never listen to, like other bots
	preRegex             *regexp.Regexp  // regex for matching prefixed commands, e.g. "Gort, drop your weapon"
	postRegex            *regexp.Regexp  // regex for matching, e.g. "open the pod bay doors, hal"
	bareRegex            *regexp.Regexp  // regex for matching the robot's bare name, if you forgot it in the previous command
	joinChannels         []string        // list of channels to join
	defaultAllowDirect   bool            // whether plugins are available in DM by default
	defaultMessageFormat MessageFormat   // Raw unless set to Variable or Fixed
	plugChannels         []string        // list of channels where plugins are available by default
	protocol             string          // Name of the protocol, e.g. "slack"
	brainProvider        string          // Type of Brain provider to use
	brain                SimpleBrain     // Interface for robot to Store and Retrieve data
	encryptionKey        string          // Key for encrypting data (unlocks "real" key in brain)
	historyProvider      string          // Name of the history provider to use
	history              HistoryProvider // Provider for storing and retrieving job / plugin histories
	workSpace            string          // Read/Write directory where the robot does work
	defaultElevator      string          // Plugin name for performing elevation
	defaultAuthorizer    string          // Plugin name for performing authorization
	externalPlugins      []ExternalTask  // List of external plugins to load
	externalJobs         []ExternalTask  // List of external jobs to load
	externalTasks        []ExternalTask  // List of external tasks to load
	ScheduledJobs        []ScheduledTask // List of scheduled tasks
	port                 string          // Localhost port to listen on
	stop                 chan struct{}   // stop channel for stopping the connector
	done                 chan struct{}   // channel closed when robot finishes shutting down
	timeZone             *time.Location  // for forcing the TimeZone, Unix only
	defaultJobChannel    string          // where job statuses will post if not otherwise specified
	shuttingDown         bool            // to prevent new plugins from starting
	pluginsRunning       int             // a count of how many plugins are currently running
	paused               bool            // it's a Windows thing
	sync.WaitGroup                       // for keeping track of running plugins
	sync.RWMutex                         // for safe updating of bot data structures
}

var listening bool // for tests where initBot runs multiple times

// initBot sets up the global robot and loads
// configuration.
func initBot(cpath, epath string, logger *log.Logger) {
	stopRegistrations = true
	// Seed the pseudo-random number generator, for plugin IDs, RandomString, etc.
	random = rand.New(rand.NewSource(time.Now().UnixNano()))

	botLogger.l = logger

	configPath = cpath
	installPath = epath
	botCfg.stop = make(chan struct{})
	botCfg.done = make(chan struct{})
	botCfg.shuttingDown = false

	handle := handler{}
	c := &botContext{
		environment: make(map[string]string),
	}
	if err := c.loadConfig(true); err != nil {
		Log(Fatal, "Error loading initial configuration: %v", err)
	}

	if len(botCfg.brainProvider) > 0 {
		if bprovider, ok := brains[botCfg.brainProvider]; !ok {
			Log(Fatal, "No provider registered for brain: \"%s\"", botCfg.brainProvider)
		} else {
			brain := bprovider(handle, logger)
			botCfg.brain = brain
		}
	} else {
		bprovider, _ := brains["mem"]
		botCfg.brain = bprovider(handle, logger)
		Log(Error, "No brain configured, falling back to default 'mem' brain - no memories will persist")
	}
	initialized := false
	if len(botCfg.encryptionKey) > 0 {
		if initializeEncryption(botCfg.encryptionKey) {
			Log(Info, "Successfully initialized encryption from configured key")
			initialized = true
		} else {
			Log(Error, "Failed to initialize brain encryption with configured EncryptionKey")
		}
	}
	if encryptBrain && !initialized {
		Log(Warn, "Brain encryption specified but not initialized; use 'initialize brain <key>' to initialize the encrypted brain interactively")
	}
	if !listening {
		listening = true
		go func() {
			h := handler{}
			http.Handle("/json", h)
			Log(Fatal, "error serving '/json': %s", http.ListenAndServe(botCfg.port, nil))
		}()
	}
}

// set connector sets the connector, which should already be initialized
func setConnector(c Connector) {
	botCfg.Lock()
	botCfg.Connector = c
	botCfg.Unlock()
}

// run starts all the loops and returns a channel that closes when the robot
// shuts down. It should return after the connector loop has started and
// plugins are initialized.
func run() <-chan struct{} {
	// Start the brain loop
	go runBrain()

	c := &botContext{
		environment: make(map[string]string),
	}
	c.registerActive(nil)
	c.loadConfig(false)
	c.deregister()

	var cl []string
	botCfg.RLock()
	cl = append(cl, botCfg.joinChannels...)
	cl = append(cl, botCfg.plugChannels...)
	cl = append(cl, botCfg.defaultJobChannel)
	botCfg.RUnlock()
	jc := make(map[string]bool)
	for _, channel := range cl {
		if _, ok := jc[channel]; !ok {
			jc[channel] = true
			botCfg.JoinChannel(channel)
		}
	}

	// signal handler
	go func() {
		botCfg.RLock()
		done := botCfg.done
		botCfg.RUnlock()
		sigs := make(chan os.Signal, 1)

		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	loop:
		for {
			select {
			case sig := <-sigs:
				botCfg.Lock()
				if botCfg.shuttingDown {
					Log(Warn, "Received SIGINT/SIGTERM while shutdown in progress")
					botCfg.Unlock()
				} else {
					botCfg.shuttingDown = true
					botCfg.Unlock()
					signal.Stop(sigs)
					Log(Info, "Exiting on signal: %s", sig)
					stop()
				}
			case <-done:
				break loop
			}
		}
	}()

	// connector loop
	botCfg.RLock()
	go func(conn Connector, stop <-chan struct{}, done chan<- struct{}) {
		privCheck("connector loop")
		conn.Run(stop)
		close(done)
	}(botCfg.Connector, botCfg.stop, botCfg.done)
	botCfg.RUnlock()
	return botCfg.done
}

// stop is called whenever the robot needs to shut down gracefully. All callers
// should lock the bot and check the value of botCfg.shuttingDown; see
// builtins.go and win_svc_run.go
func stop() {
	botCfg.RLock()
	pr := botCfg.pluginsRunning
	stop := botCfg.stop
	botCfg.RUnlock()
	Log(Debug, "stop called with %d plugins running", pr)
	botCfg.Wait()
	brainQuit()
	close(stop)
}
