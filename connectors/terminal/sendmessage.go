// +build !test

package terminal

import (
	"fmt"
	"strings"

	"github.com/wanghonggao007/gopherbot/bot"
)

func (tc *termConnector) sendMessage(ch, msg string, f bot.MessageFormat) (ret bot.RetVal) {
	found := false
	tc.RLock()
	if strings.HasPrefix(ch, "(dm:") {
		found = true
	} else {
		for _, channel := range tc.channels {
			if channel == ch {
				found = true
				break
			}
		}
	}
	tc.RUnlock()
	if !found {
		tc.Log(bot.Error, "Channel not found:", ch)
		return bot.ChannelNotFound
	}
	tc.reader.Write([]byte(fmt.Sprintf("%s: %s\n", ch, msg)))
	return bot.Ok
}
