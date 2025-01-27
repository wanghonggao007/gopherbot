package bot

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

/*

Job builtins are special:
- They're available in every channel
- Permissions are checked against the job being operated on, not job builtin

*/

const histPageSize = 2048    // how much history to display at a time
const maxMailBody = 10485760 // 10MB

func init() {
	RegisterPlugin("builtin-history", PluginHandler{Handler: jobhistory})
	RegisterPlugin("builtin-jobcmd", PluginHandler{Handler: jobcommands})
}

func jobcommands(r *Robot, command string, args ...string) (retval TaskRetVal) {
	if command == "init" {
		return
	}
	switch command {
	case "jobs":
		var jl []string
		alljobs := len(args[0]) > 0
		if alljobs {
			jl = []string{"Here's a list of all the jobs I know about:"}
		} else {
			jl = []string{"Here's a list of jobs for this channel:"}
		}
		c := r.getContext()
		for _, t := range c.tasks.t {
			if !r.jobVisible(t, alljobs, true) {
				continue
			}
			task, _, _ := getTask(t)
			after := ""
			if task.Disabled {
				after = fmt.Sprintf(" (disabled: %s)", task.reason)
			}
			if alljobs && r.Channel != task.Channel {
				jl = append(jl, fmt.Sprintf("%s (channel: %s)%s", task.name, task.Channel, after))
			} else {
				jl = append(jl, fmt.Sprintf("%s%s", task.name, after))
			}
		}
		if len(jl) == 1 {
			if alljobs {
				r.Say("I dont' have any jobs configured")
				return
			}
			r.Say("I don't see any jobs configured for this channel")
			return
		}
		r.Say(strings.Join(jl, "\n"))
	}
	return
}

func emailhistory(r *Robot, hp HistoryProvider, user, address, spec string, run int) (retval TaskRetVal) {
	f, err := hp.GetHistory(spec, run)
	if err != nil {
		Log(Error, "Error getting history %d for task '%s': %v", run, spec, err)
		r.Say(fmt.Sprintf("History %d for '%s' not available", run, spec))
		return
	}
	lr := io.LimitReader(f, maxMailBody)
	body := new(bytes.Buffer)
	body.Write([]byte("<pre>\n"))
	b, rerr := ioutil.ReadAll(lr)
	if rerr != nil {
		r.Log(Error, "reading history #%d for '%s': %v", run, spec, rerr)
		r.Reply("There was a problem reading the history, check with an administrator")
		return
	}
	body.Write(b)
	body.Write([]byte("\n</pre>"))
	subject := fmt.Sprintf("History for '%s', run %d", spec, run)
	var ret RetVal
	if len(user) > 0 {
		ret = r.EmailUser(user, subject, body, true)
	} else if len(address) > 0 {
		ret = r.EmailAddress(address, subject, body, true)
	} else {
		ret = r.Email(subject, body, true)
	}
	if ret != Ok {
		r.Reply("There was a problem emailing the history log, contact an administrator")
		return
	}
	r.Say("Email sent")
	return
}

func pagehistory(r *Robot, hp HistoryProvider, spec string, run int) (retval TaskRetVal) {
	f, err := hp.GetHistory(spec, run)
	if err != nil {
		Log(Error, "Error getting history %d for task '%s': %v", run, spec, err)
		r.Say(fmt.Sprintf("History %d for '%s' not available", run, spec))
		return
	}
	var line string
	scanner := bufio.NewScanner(f)
	finished := false
PageLoop:
	for {
		size := 0
		lines := make([]string, 0, 40)
		if len(line) > 0 {
			lines = append(lines, line)
			size += len(line) + 1
			line = ""
		}
		for size < histPageSize {
			if scanner.Scan() {
				line = scanner.Text()
				size += len(line) + 1
				if size < histPageSize {
					lines = append(lines, line)
					line = ""
				}
			} else {
				finished = true
				break
			}
		}
		r.Fixed().Say(strings.Join(lines, "\n"))
		if finished {
			break
		}
		rep, ret := r.PromptForReply("paging", "'c' to continue, 'q' to quit, or 'n' to skip to the next section")
		if ret != Ok {
			r.Say("(quitting)")
			break PageLoop
		} else {
		ContinueSwitch:
			switch rep {
			case "q", "Q":
				r.Say("(ok, quitting)")
				break PageLoop
			case "n", "N":
				for scanner.Scan() {
					line = scanner.Text()
					if strings.HasPrefix(line, "***") {
						break ContinueSwitch
					}
				}
			}
		}
	}
	return
}

func jobhistory(r *Robot, command string, args ...string) (retval TaskRetVal) {
	if command == "init" {
		return
	}

	var histType, latest, histSpec, index, user, address string

	switch command {
	case "history":
		histType = args[0]
		latest = args[1]
		histSpec = args[2]
		index = args[3]
	case "mailhistory":
		histType = "email"
		latest = args[0]
		histSpec = args[1]
		index = args[2]
		user = args[3]
		address = args[4]
	}

	// boilerplate availability and security checking for job commands
	c := r.getContext()
	jobName := strings.Split(histSpec, ":")[0]
	t := c.jobAvailable(jobName)
	if t == nil {
		return
	}
	if !c.jobSecurityCheck(t, command) {
		return
	}
	vr := r.MessageFormat(Variable)

	switch command {
	case "history", "mailhistory":
		botCfg.RLock()
		hp := botCfg.history
		if hp == nil {
			botCfg.RUnlock()
			r.Reply("No history provider configured")
			return
		}
		botCfg.RUnlock()
		var jh jobHistory
		key := histPrefix + histSpec
		_, _, ret := checkoutDatum(key, &jh, false)
		if ret != Ok {
			r.Say(fmt.Sprintf("No history found for '%s'", histSpec))
			return
		}
		if len(latest) == 0 && len(index) == 0 {
			if len(jh.ExtendedNamespaces) > 0 {
				nsl := make([]string, len(jh.ExtendedNamespaces)+2)
				nsl = append(nsl, fmt.Sprintf("Namespaces for %s:", histSpec))
				if len(jh.Histories) > 0 {
					nsl = append(nsl, "0: (base job)")
				}
				for i, ens := range jh.ExtendedNamespaces {
					nsl = append(nsl, fmt.Sprintf("%d: %s", i+1, ens))
				}
				vr.Say(strings.Join(nsl, "\n"))
				rep, ret := r.PromptForReply("selection", "Which namespace #?")
				if ret != Ok {
					r.Say("(quitting history command)")
					return
				}
				if rep != "0" {
					i, _ := strconv.Atoi(rep)
					histSpec += ":" + jh.ExtendedNamespaces[i-1]
					key = histPrefix + histSpec
					_, _, ret = checkoutDatum(key, &jh, false)
				}
			}
		}
		if len(jh.Histories) == 0 {
			r.Say(fmt.Sprintf("No history found for '%s'", histSpec))
			return
		}

		// remember which job we're talking about
		ctx := memoryContext{"context:task", r.User, r.Channel}
		s := shortTermMemory{histSpec, time.Now()}
		shortTermMemories.Lock()
		shortTermMemories.m[ctx] = s
		shortTermMemories.Unlock()

		var idx int
		if len(latest) == 0 && len(index) == 0 {
			hl := make([]string, len(jh.Histories)+1)
			hl = append(hl, fmt.Sprintf("History of job runs for '%s':", histSpec))
			for _, he := range jh.Histories {
				hl = append(hl, fmt.Sprintf("Run %d - %s", he.LogIndex, he.CreateTime))
			}
			vr.Say(strings.Join(hl, "\n"))
			rep, ret := r.PromptForReply("selection", "Which run #?")
			if ret != Ok {
				r.Say("(quitting history command)")
				return
			}
			idx, _ = strconv.Atoi(rep)
		} else if len(latest) > 0 {
			idx = jh.NextIndex - 1
			if idx < 0 {
				idx = 0
			}
		} else {
			idx, _ = strconv.Atoi(index)
		}
		switch histType {
		case "mail", "email":
			if len(user) > 0 {
				return emailhistory(r, hp, user, "", histSpec, idx)
			} else if len(address) > 0 {
				return emailhistory(r, hp, "", address, histSpec, idx)
			} else {
				return emailhistory(r, hp, "", "", histSpec, idx)
			}
		case "link":
			if link, ok := hp.GetHistoryURL(histSpec, idx); ok {
				r.Say(fmt.Sprintf("Here you go: %s", link))
				return
			}
			r.Say("No link available")
			return
		default:
			return pagehistory(r, hp, histSpec, idx)
		}
	}
	return
}

// jobSecurityCheck performs all security checks - RequireAdmin, Authorization
// and Elevation - and returns true if passed. It will message the user and
// return false if a check fails.
func (c *botContext) jobSecurityCheck(t interface{}, command string) bool {
	if c.automaticTask {
		return true
	}
	ct := c.currentTask
	task, _, _ := getTask(t)
	if task.RequireAdmin {
		r := c.makeRobot()
		if !r.CheckAdmin() {
			r.Say("Sorry, that command is only available to bot administrators")
			return false
		}
	}
	if c.checkAuthorization(t, command) != Success {
		return false
	}
	if !c.elevated {
		eret, required := c.checkElevation(t, command)
		if eret != Success {
			return false
		}
		if required {
			c.elevated = true
		}
	}
	// Restore currentTask, potentially modified by checkAuthorization/checkElevation
	c.currentTask = ct
	return true
}

// jobVisible checks whether a user should see a job in a channel, unless
// ignoreChannelRestrictions is set. Note that changes to logic in jobVisible
// may need to propagate to jobAvailable, below.
func (r *Robot) jobVisible(t interface{}, ignoreChannelRestrictions, disabledOk bool) bool {
	task, _, job := getTask(t)
	if job == nil {
		return false
	}
	if task.Disabled && !disabledOk {
		return false
	}
	if !ignoreChannelRestrictions && r.Channel != task.Channel {
		return false
	}
	if len(task.Users) > 0 {
		userOk := false
		for _, allowedUser := range task.Users {
			match, err := filepath.Match(allowedUser, r.User)
			if match && err == nil {
				userOk = true
			}
		}
		if !userOk {
			return false
		}
	}
	if task.RequireAdmin {
		isAdmin := false
		botCfg.RLock()
		admins := botCfg.adminUsers
		botCfg.RUnlock()
		for _, adminUser := range admins {
			if r.User == adminUser {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			return false
		}
	}
	return true
}

// jobAvailable does the work of looking up a job and checking whether it's
// available, and messaging the user if it's not. Only called for interactive
// job commands like history, run job, etc. where the user provides a job name.
// Note that changes to login in jobAvailable may need to propagate to
// jobVisible, above.
func (c *botContext) jobAvailable(taskName string) interface{} {
	r := c.makeRobot()
	t := c.tasks.getTaskByName(taskName)
	if t == nil {
		r.Say(fmt.Sprintf("Sorry, I don't have a task named '%s' configured", taskName))
		return nil
	}
	task, _, job := getTask(t)
	isJob := job != nil
	if !isJob {
		r.Say(fmt.Sprintf("Sorry, '%s' isn't a job", taskName))
		return nil
	}
	if c.automaticTask {
		return t
	}
	// If there's already a job initialized, this is a pipeline task for that
	// job, and should be available regardless of channel.
	if !c.jobInitialized && r.Channel != task.Channel {
		c.debugTask(task, fmt.Sprintf("not available in channel '%s'", task.Channel), false)
		r.Say(fmt.Sprintf("Sorry, job '%s' isn't available in this channel, try '%s'", taskName, task.Channel))
		return nil
	}
	if task.RequireAdmin {
		isAdmin := false
		botCfg.RLock()
		admins := botCfg.adminUsers
		botCfg.RUnlock()
		for _, adminUser := range admins {
			if r.User == adminUser {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			r.Say(fmt.Sprintf("Sorry, '%s' is only available to bot administrators", taskName))
			return nil
		}
	}
	if len(task.Users) > 0 {
		userOk := false
		for _, allowedUser := range task.Users {
			match, err := filepath.Match(allowedUser, r.User)
			if match && err == nil {
				userOk = true
			}
		}
		if !userOk {
			r.Say("Sorry, you're not on the list of allowed users for that job")
			c.debugTask(task, "user is not on the list of allowed users", false)
			return nil
		}
	}
	return t
}
