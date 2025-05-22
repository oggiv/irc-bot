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
    helpMsg := "Available commands: .echo, .help, .seen, .tell"
    irccon.Privmsg(e.Arguments[0], helpMsg)
}

func seenHandler(db *sql.DB) HandlerFunc {
    return func(e *irc.Event, irccon *irc.Connection, args []string) {
        if len(args) == 0 {
            irccon.Privmsg(e.Arguments[0], "Usage: .seen <nickname>")
            return
        }

        target := args[0]
        channel := e.Arguments[0]
        var lastMessage string
        var lastSeen time.Time


        err := db.QueryRow(`
            SELECT last_message, last_seen 
            FROM user_activity 
            WHERE nickname = ? AND channel = ?
            ORDER BY last_seen DESC 
            LIMIT 1
        `, target, channel).Scan(&lastMessage, &lastSeen)
        
        if err == sql.ErrNoRows {
            irccon.Privmsg(channel, fmt.Sprintf("I haven't seen %s around.", target))
            return
        } else if err != nil {
            log.Printf("DB error in seenHandler: %v", err)
            irccon.Privmsg(channel, "Error looking up user.")
            return
        }

        irccon.Privmsg(channel, 
            fmt.Sprintf("%s was last seen at %s saying: \"%s\"", 
                target, 
                lastSeen.Format("2006-01-02 15:04:05"), 
                lastMessage))
    }
}

func tellHandler(db *sql.DB) HandlerFunc {
    return func(e *irc.Event, irccon *irc.Connection, args []string) {
        if len(args) < 2 {
            irccon.Privmsg(e.Arguments[0], "Usage: .tell <nickname> <message>")
            return
        }

        recipient := args[0]
        message := strings.Join(args[1:], " ")
        sender := e.Nick
        channel := e.Arguments[0]

        // Check existing message count
        var count int
        err := db.QueryRow(`
            SELECT COUNT(*) 
            FROM tell_messages 
            WHERE sender = ? AND recipient = ? AND channel = ? AND delivered = FALSE
        `, sender, recipient, channel).Scan(&count)
        
        if err != nil {
            log.Printf("DB error in tellHandler count: %v", err)
            irccon.Privmsg(channel, "Error checking message count.")
            return
        }

        if 5 <= count {
            irccon.Privmsg(channel, 
                fmt.Sprintf("%s: maximum .tell to %s already reached", sender, recipient))
            return
        }

        // Insert new tell message
        _, err = db.Exec(`
            INSERT INTO tell_messages 
            (sender, recipient, channel, message) 
            VALUES (?, ?, ?, ?)
        `, sender, recipient, channel, message)
        
        if err != nil {
            log.Printf("DB error in tellHandler insert: %v", err)
            irccon.Privmsg(channel, "Error saving your message.")
            return
        }

        irccon.Privmsg(channel, 
            fmt.Sprintf("%s: I'll pass your message on to %s.", sender, recipient))
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
        "help": helpHandler, // Also update this!
        "seen": seenHandler(db),
        "tell": tellHandler(db),
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

        // Check for pending tell messages
        var messages []struct {
            id        int
            sender    string
            message   string
            createdAt time.Time
        }

        rows, err := db.Query(`
            SELECT id, sender, message, created_at 
            FROM tell_messages 
            WHERE recipient = ? AND channel = ? AND delivered = FALSE
        `, e.Nick, e.Arguments[0])
        if err != nil {
            log.Printf("Error querying tell messages: %v", err)
        } else {
            defer rows.Close()
            
            // Process rows and store in memory
            for rows.Next() {
                var msg struct {
                    id        int
                    sender    string
                    message   string
                    createdAt time.Time
                }
                if err := rows.Scan(&msg.id, &msg.sender, &msg.message, &msg.createdAt); err != nil {
                    log.Printf("Error scanning tell message: %v", err)
                    continue
                }
                messages = append(messages, msg)
            }
            rows.Close()

            // Deliver messages and mark as delivered
            for _, msg := range messages {
                // Deliver the message
                irccon.Privmsg(e.Arguments[0], 
                    fmt.Sprintf("%s: \"%s\" ~ %s [%s]", 
                        e.Nick, 
                        msg.message, 
                        msg.sender, 
                        msg.createdAt.Format("2006-01-02 15:04")))
                
                // Mark as delivered
                _, err = db.Exec(`
                    UPDATE tell_messages 
                    SET delivered = TRUE 
                    WHERE id = ?
                `, msg.id)
                
                if err != nil {
                    log.Printf("Error marking message delivered: %v", err)
                }
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