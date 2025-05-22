package main

import (
        "crypto/tls"
        "fmt"
        "strings"
        irc "github.com/thoj/go-ircevent"
)

const (
        servername = "irc.rizon.net"
        serverport = "6697"
        
        channel = "#go-eventirc-test";

        botname = "mega_test_bot"
        botident = "m_test_bot"
        prefix = "."
)

func echoHandler(e *irc.Event, irccon *irc.Connection, args []string) {
    if len(args) == 0 {
        irccon.Privmsg(e.Arguments[0], "Usage: .echo <message>")
        return
    }
    irccon.Privmsg(e.Arguments[0], fmt.Sprintf("%s said: %s", e.Nick, strings.Join(args, " ")))
}

func helpHandler(e *irc.Event, irccon *irc.Connection, args []string) {
    helpMsg := "Available commands: .echo, .help"
    irccon.Privmsg(e.Arguments[0], helpMsg)
}

func main() {
        irccon := irc.IRC(botname, botident)
        irccon.VerboseCallbackHandler = true
        irccon.Debug = true
        irccon.UseTLS = true
        irccon.TLSConfig = &tls.Config{
                ServerName: servername,
                InsecureSkipVerify: false,
        }

        // Command map
        commands := map[string]func(*irc.Event, *irc.Connection, []string) {
                "echo": echoHandler,
                "help": helpHandler,
        }

        // Join channel on connect
        irccon.AddCallback("001", func(e *irc.Event) {
                irccon.Join(channel)
        })

        // Do nothing (?) at the end of the nickname
        irccon.AddCallback("366", func(e *irc.Event) {  })

        // Handle commands
        irccon.AddCallback("PRIVMSG", func(e *irc.Event) {
                // Only respond to channel messages (not private messages)
                if !strings.HasPrefix(e.Arguments[0], "#") {
                        // Skip messages that aren't from a channel
                        return
                }

                msg := strings.TrimSpace(e.Message())
                if !strings.HasPrefix(msg, prefix) {
                        // Skip messages that don't begin with the bot prefix
                        return
                }

                parts := strings.Fields(msg[len(prefix):])
                if len(parts) == 0 {
                        // Skip commands that are empty
                        return
                }

                cmd := strings.ToLower(parts[0])
                args := parts[1:]

                if handler, exists := commands[cmd]; exists {
                        handler(e, irccon, args)
                }
        })

        err := irccon.Connect(servername + ":" + serverport)
        if err != nil {
                fmt.Printf("Err %s", err )
                return
        }
        irccon.Loop()
}