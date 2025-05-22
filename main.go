package main

import (
    "crypto/tls"
    "database/sql"
    "fmt"
    "log"
    "strings"
    "time"
    _ "github.com/mattn/go-sqlite3"
    irc "github.com/thoj/go-ircevent"
)

const (
    servername = "irc.rizon.net"
    serverport = "6697"
    
    channel = "#go-eventirc-test";

    botname = "mega_test_bot"
    botident = "m_test_bot"
    prefix = "."

    DBPath = "./irc-bot.db"
)

// Handler functions for commands
type HandlerFunc func(e *irc.Event, irccon *irc.Connection, args []string)

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

func testHandler(db *sql.DB) HandlerFunc {
    return func(e *irc.Event, irccon *irc.Connection, args []string) {
        // Test connection
        err := db.Ping()
        if err != nil { log.Fatal("test cmd error:", err) }
        irccon.Privmsg(e.Arguments[0], "SQLite connected!")
    }
}

func seenHandler(db *sql.DB) HandlerFunc {
    return func(e *irc.Event, irccon *irc.Connection, args []string) {
        if len(args) == 0 {
            irccon.Privmsg(e.Arguments[0], "Usage: .seen <nickname>")
            return
        }

        target := args[0]
        var lastMessage string
        var lastSeen time.Time


        err := db.QueryRow(`
            SELECT last_message, last_seen 
            FROM user_activity 
            WHERE nickname = ? AND channel = ?
            ORDER BY last_seen DESC 
            LIMIT 1
        `, target, e.Arguments[0]).Scan(&lastMessage, &lastSeen)
        
        if err == sql.ErrNoRows {
            irccon.Privmsg(e.Arguments[0], fmt.Sprintf("I haven't seen %s around.", target))
            return
        } else if err != nil {
            log.Printf("DB error in seenHandler: %v", err)
            irccon.Privmsg(e.Arguments[0], "Error looking up user.")
            return
        }

        irccon.Privmsg(e.Arguments[0], 
            fmt.Sprintf("%s was last seen at %s saying: \"%s\"", 
                target, 
                lastSeen.Format("2006-01-02 15:04:05"), 
                lastMessage))
    }
}

// Database functions
func initDB(path string) *sql.DB {
    // Open DB and create one if needed
    db, err := sql.Open("sqlite3", path)
    if err != nil { log.Fatal("DB open error:", err) }

    // Create tables
    _, err = db.Exec(`
        CREATE TABLE IF NOT EXISTS user_activity (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            nickname TEXT NOT NULL,
            channel TEXT NOT NULL,
            last_message TEXT,
            last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            UNIQUE(nickname, channel)
        );
        
        CREATE TABLE IF NOT EXISTS tell_messages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            sender TEXT NOT NULL,
            recipient TEXT NOT NULL,
            channel TEXT NOT NULL,
            message TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            delivered BOOLEAN DEFAULT FALSE
        );
        
        CREATE INDEX IF NOT EXISTS idx_tell_recipient ON tell_messages(recipient, delivered);
        CREATE INDEX IF NOT EXISTS idx_user_activity ON user_activity(nickname);
    `)
    if err != nil { log.Fatal("DB init error:", err) }

    return db
}

// Main
func main() {
    irccon := irc.IRC(botname, botident)
    irccon.VerboseCallbackHandler = true
    irccon.Debug = true
    irccon.UseTLS = true
    irccon.TLSConfig = &tls.Config{
        ServerName: servername,
        InsecureSkipVerify: false,
    }

    // Database
    db := initDB(DBPath)
    defer db.Close()

    // Command map
    commands := map[string]func(*irc.Event, *irc.Connection, []string) {
        "echo": echoHandler,
        "help": helpHandler,
        "seen": seenHandler(db),
        "test": testHandler(db),
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

        // Log user activity for .seen command
        if strings.HasPrefix(e.Arguments[0], "#") {
            _, err := db.Exec(`
                INSERT INTO user_activity (nickname, channel, last_message, last_seen)
                VALUES (?, ?, ?, CURRENT_TIMESTAMP)
                ON CONFLICT(nickname, channel) DO UPDATE SET
                    last_message = excluded.last_message,
                    last_seen = excluded.last_seen
            `, e.Nick, e.Arguments[0], e.Message())
            if err != nil {
                log.Printf("Error updating user activity: %v", err)
            }
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