package bot

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// MessageFormat indicates how the connector should display the content of
// the message. One of Variable, Fixed or Raw
type MessageFormat int

// Outgoing message format, Variable or Fixed
const (
	Raw MessageFormat = iota // protocol native, zero value -> default if not specified
	Fixed
	Variable
)

// Robot is passed to each task as it runs, initialized from the botContext.
// Tasks can copy and modify the Robot without affecting the botContext.
type Robot struct {
	User            string            // The user who sent the message; this can be modified for replying to an arbitrary user
	ProtocolUser    string            // the protocol internal ID of the user
	Channel         string            // The channel where the message was received, or "" for a direct message. This can be modified to send a message to an arbitrary channel.
	ProtocolChannel string            // the protocol internal channel ID
	Protocol        Protocol          // slack, terminal, test, others; used for interpreting rawmsg or sending messages with Format = 'Raw'
	Incoming        *ConnectorMessage // raw struct of message sent by connector; interpret based on protocol. For Slack this is a *slack.MessageEvent
	Format          MessageFormat     // The outgoing message format, one of Raw, Fixed, or Variable
	id              int               // For looking up the botContext
}

/* robot_methods.go defines some convenience functions on struct Robot to
   simplify use by plugins. */

// getContext returns the botContext for a given Robot
func (r *Robot) getContext() *botContext {
	return getBotContextInt(r.id)
}

// CheckAdmin returns true if the user is a configured administrator of the
// robot, and true for automatic tasks. Should be used sparingly, when a single
// plugin has multiple commands, some which require admin. Otherwise the plugin
// should just configure RequireAdmin: true
func (r *Robot) CheckAdmin() bool {
	c := r.getContext()
	if c.automaticTask {
		return true
	}
	botCfg.RLock()
	defer botCfg.RUnlock()
	for _, adminUser := range botCfg.adminUsers {
		if r.User == adminUser {
			emit(AdminCheckPassed)
			return true
		}
	}
	emit(AdminCheckFailed)
	return false
}

// SetParameter sets a parameter for the current pipeline, useful only for
// passing parameters (as environment variables) to tasks later in the pipeline.
func (r *Robot) SetParameter(name, value string) bool {
	if !identifierRe.MatchString(name) {
		return false
	}
	c := r.getContext()
	c.environment[name] = value
	return true
}

// GetSecret looks up the value of a secret for the namespace (if the namespace
// is extended) or current task. On error a zero-length string is returned.
func (r *Robot) GetSecret(name string) string {
	cryptKey.RLock()
	initialized := cryptKey.initialized
	key := cryptKey.key
	cryptKey.RUnlock()
	if !initialized {
		r.Log(Warn, "GetSecret called but encryption not initialized")
		return ""
	}

	var secret []byte
	var exists bool
	var ret RetVal

	c := r.getContext()
	if !c.secrets.retrieved {
		// if it fails, there's little point in multiple lookups in a single
		// pipeline
		c.secrets.retrieved = true
		_, exists, ret = checkoutDatum(secretKey, &c.secrets, false)
		if ret != Ok {
			r.Log(Error, "Error retrieving secrets in GetSecret: %s", ret)
			return ""
		}
		if !exists {
			r.Log(Warn, "GetSecret called for '%s', but no secrets stored", name)
			return ""
		}
	}
	task, _, _ := getTask(c.currentTask)
	secfound := false
	if len(c.nsExtension) > 0 {
		var nsMap map[string][]byte
		found := false
		nsMap, exists = c.secrets.RepositoryParams[c.nsExtension]
		if exists {
			found = true
			if secret, exists = nsMap[name]; exists {
				secfound = true
			}
		}
		if !secfound {
			cmp := strings.Split(c.nsExtension, "/")
			repo := strings.Join(cmp[0:len(cmp)-1], "/")
			nsMap, exists = c.secrets.RepositoryParams[repo]
			if exists {
				found = true
				if secret, exists = nsMap[name]; exists {
					secfound = true
				}
			}
		}
		if !found {
			r.Log(Debug, "Secrets not found for extended namespace '%s'", c.nsExtension)
		} else if !secfound {
			r.Log(Debug, "Secret '%s' not found for extended namespace '%s'", name, c.nsExtension)
		}
	}
	// Fall back to task secrets if namespace secret not found
	if !secfound {
		var tMap map[string][]byte
		tMap, exists = c.secrets.TaskParams[task.NameSpace]
		if !exists {
			r.Log(Debug, "Secrets not found for task/namespace '%s'", task.NameSpace)
		} else if secret, exists = tMap[name]; !exists {
			r.Log(Debug, "Secret '%s' not found for task/namespace '%s'", name, task.NameSpace)
		} else {
			secfound = true
		}
	}
	if !secfound {
		r.Log(Warn, "Secret '%s' not found for extended namespace '%s' or task/namespace '%s'", name, c.nsExtension, task.NameSpace)
		return ""
	}
	var value []byte
	var err error
	if value, err = decrypt(secret, key); err != nil {
		r.Log(Error, "Error decrypting secret '%s': %v", name, err)
		return ""
	}
	return string(value)
}

// SetWorkingDirectory sets the working directory of the pipeline for all scripts
// executed. The path argument can be absolute or relative; if relative, it is
// always relative to the robot's WorkSpace.
func (r *Robot) SetWorkingDirectory(path string) bool {
	c := r.getContext()
	if path == "." {
		c.workingDirectory = ""
		return true
	}
	if filepath.IsAbs(path) {
		_, ok := checkDirectory(path)
		if ok {
			c.workingDirectory = path
		} else {
			r.Log(Error, "Invalid path '%s' in SetWorkingDirectory", path)
		}
		return ok
	}
	var prefix, checkPath string
	if c.protected {
		prefix = configPath
	} else {
		botCfg.RLock()
		prefix = botCfg.workSpace
		botCfg.RUnlock()
	}
	checkPath = filepath.Join(prefix, path)
	_, ok := checkDirectory(checkPath)
	if ok {
		c.workingDirectory = path
	} else {
		r.Log(Error, "Invalid path '%s'(%s) in SetWorkingDirectory", path, checkPath)
	}
	return ok
}

// GetParameter retrieves the value of a parameter for a namespace. Only useful
// for Go plugins; external scripts have all parameters for the NameSpace stored
// as environment variables. Note that runtasks.go populates the environment
// with Stored parameters, too. So GetParameter is useful for both short-term
// parameters in a pipeline, and for getting long-term parameters such as
// credentials.
func (r *Robot) GetParameter(key string) string {
	c := r.getContext()
	value, ok := c.taskenvironment[key]
	if ok {
		return value
	}
	return ""
}

// Elevate lets a plugin request elevation on the fly. When immediate = true,
// the elevator should always prompt for 2fa; otherwise a configured timeout
// should apply.
func (r *Robot) Elevate(immediate bool) bool {
	c := r.getContext()
	task, _, _ := getTask(c.currentTask)
	retval := c.elevate(task, immediate)
	if retval == Success {
		return true
	}
	return false
}

// Fixed is a deprecated convenience function for sending a message with fixed width
// font.
func (r *Robot) Fixed() *Robot {
	nr := *r
	nr.Format = Fixed
	return &nr
}

// MessageFormat returns a robot object with the given format, most likely for a
// plugin that will mostly use e.g. Variable format.
func (r *Robot) MessageFormat(f MessageFormat) *Robot {
	nr := *r
	nr.Format = f
	return &nr
}

// Direct is a convenience function for initiating a DM conversation with a
// user. Created initially so a plugin could prompt for a password in a DM.
func (r *Robot) Direct() *Robot {
	nr := *r
	nr.Channel = ""
	return &nr
}

// Pause is a convenience function to pause some fractional number of seconds.
func (r *Robot) Pause(s float64) {
	ms := time.Duration(s * float64(1000))
	time.Sleep(ms * time.Millisecond)
}

// RandomString is a convenience function for returning a random string
// from a slice of strings, so that replies can vary.
func (r *Robot) RandomString(s []string) string {
	l := len(s)
	if l == 0 {
		return ""
	}
	return s[random.Intn(l)]
}

// RandomInt uses the robot's seeded random to return a random int 0 <= retval < n
func (r *Robot) RandomInt(n int) int {
	return random.Intn(n)
}

// GetBotAttribute returns an attribute of the robot or "" if unknown.
// Current attributes:
// name, alias, fullName, contact
func (r *Robot) GetBotAttribute(a string) *AttrRet {
	a = strings.ToLower(a)
	botCfg.RLock()
	defer botCfg.RUnlock()
	ret := Ok
	var attr string
	switch a {
	case "name":
		attr = botCfg.botinfo.UserName
	case "fullname", "realname":
		attr = botCfg.botinfo.FullName
	case "alias":
		attr = string(botCfg.alias)
	case "mail", "email":
		attr = botCfg.botinfo.Email
	case "contact", "admin", "admincontact":
		attr = botCfg.adminContact
	case "protocol":
		attr = r.Protocol.String()
	default:
		ret = AttributeNotFound
	}
	return &AttrRet{attr, ret}
}

/*

GetTaskConfig sets a struct pointer to point to a config struct populated
from configuration when plugins were loaded. To use, a plugin should define
a struct for it's configuration data, e.g.:

	type pConf struct {
		Username, Password string
	}

In conf/plugins/<pluginname>.yaml, you would add a Config: stanza, e.g.:

	Config:
	  Username: foo
	  Password: bar

When registering the plugin, you pass a pointer to an empty config template, which the
robot will use to populate a struct when configuration is loaded:

	func init() {
		bot.RegisterPlugin("memes", bot.PluginHandler{
			DefaultConfig: defaultConfig, // yaml string providing default configuration
			Handler:       plugfunc, // callback function
			Config:        &pConf{}, // pointer to empty config struct
		})
	}

Then, to get a current copy of configuration when the plugin runs, define a struct pointer
and call GetTaskConfig with a double-pointer:

	var c *pConf
	r.GetTaskConfig(&c)

... And voila! *pConf is populated with the contents from the configured Config: stanza
*/
func (r *Robot) GetTaskConfig(dptr interface{}) RetVal {
	c := r.getContext()
	task, _, _ := getTask(c.currentTask)
	if task.config == nil {
		Log(Debug, "Task \"%s\" called GetTaskConfig, but no config was found.", task.name)
		return NoConfigFound
	}
	tp := reflect.ValueOf(dptr)
	if tp.Kind() != reflect.Ptr {
		Log(Debug, "Task \"%s\" called GetTaskConfig, but didn't pass a double-pointer to a struct", task.name)
		return InvalidDblPtr
	}
	p := reflect.Indirect(tp)
	if p.Kind() != reflect.Ptr {
		Log(Debug, "Task \"%s\" called GetTaskConfig, but didn't pass a double-pointer to a struct", task.name)
		return InvalidDblPtr
	}
	if p.Type() != reflect.ValueOf(task.config).Type() {
		Log(Debug, "Task \"%s\" called GetTaskConfig with an invalid double-pointer", task.name)
		return InvalidCfgStruct
	}
	p.Set(reflect.ValueOf(task.config))
	return Ok
}

// Log logs a message to the robot's log file (or stderr) if the level
// is lower than or equal to the robot's current log level
func (r *Robot) Log(l LogLevel, m string, v ...interface{}) {
	c := r.getContext()
	if Log(l, m, v...) && c.logger != nil {
		line := "LOG " + logLevelToStr(l) + " " + fmt.Sprintln(v...)
		c.logger.Log(strings.TrimSpace(line))
	}
}
