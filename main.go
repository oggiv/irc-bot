package main

import (
        "github.com/thoj/go-ircevent"
        "crypto/tls"
        "fmt"
)

const channel = "#go-eventirc-test";
const servername = "irc.rizon.net"
const serverport = "6697"

const botname = "mega_test_bot"

func main() {
        irccon := irc.IRC(botname, "IRCTestSSL")
        irccon.VerboseCallbackHandler = true
        irccon.Debug = true
        irccon.UseTLS = true
        irccon.TLSConfig = &tls.Config{
                ServerName: servername,
                InsecureSkipVerify: false,
        }

        // Join channel on connect
        irccon.AddCallback("001", func(e *irc.Event) { irccon.Join(channel) })

        // Do nothing (?) at the end of the nickname
        irccon.AddCallback("366", func(e *irc.Event) {  })

        err := irccon.Connect(servername + ":" + serverport)
        if err != nil {
                fmt.Printf("Err %s", err )
                return
        }
        irccon.Loop()
}