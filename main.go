package main

import (
    "bufio"
    "crypto/tls"
    "database/sql"
    "fmt"
    "log"
    "os"
    "strings"
    "time"
    _ "github.com/mattn/go-sqlite3"
    irc "github.com/thoj/go-ircevent"
)

const (
    servername = "irc.libera.chat"
    serverport = "6697"
    UseTLS = true

    channel = "#go-irc-bot-test";
    botname = "test-bot"
    botident = "test-bot"
    prefix = "."

    UseSASL = false // change
    SASLMech = "PLAIN"
    SASLPath = "sasl.txt"

    DBPath = "bot-data.db"
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

        target := strings.ToLower(args[0])
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

        recipient := strings.ToLower(args[0])
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
            fmt.Sprintf("%s: I'll pass your message on to %s.", sender, args[0]))
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

// SASL helper functions
type SASLCredentials struct {
    Login    string
    Password string
}

func readSASLCredentials(filename string) (SASLCredentials, error) {
    file, err := os.Open(filename)
    if err != nil {
        return SASLCredentials{}, err
    }
    defer file.Close()

    scanner := bufio.NewScanner(file)
    var lines []string
    for scanner.Scan() {
        lines = append(lines, strings.TrimSpace(scanner.Text()))
    }

    if len(lines) < 2 {
        return SASLCredentials{}, fmt.Errorf("invalid file format: need login and password on separate lines")
    }

    return SASLCredentials{
        Login:    lines[0],
        Password: lines[1],
    }, nil
}

func promptSASLCredentials() SASLCredentials {
    reader := bufio.NewReader(os.Stdin)

    fmt.Print("Enter SASL login: ")
    login, _ := reader.ReadString('\n')
    login = strings.TrimSpace(login)

    fmt.Print("Enter SASL password: ")
    password, _ := reader.ReadString('\n')
    password = strings.TrimSpace(password)

    return SASLCredentials{
        Login:    login,
        Password: password,
    }
}

func setupSASL(irccon *irc.Connection) {
    creds, err := readSASLCredentials(SASLPath)
    if err != nil {
        log.Printf("Couldn't read SASL credentials from file (%v)", err)
        creds = promptSASLCredentials()

        // Optionally save for next time
        if file, err := os.Create(SASLPath); err == nil {
            defer file.Close()
            fmt.Fprintf(file, "%s\n%s\n", creds.Login, creds.Password)
            log.Println("Saved SASL credentials to", SASLPath)
        } else {
            log.Printf("Warning: couldn't save SASL credentials: %v", err)
        }
    }

    // Configure SASL
    irccon.SASLLogin = creds.Login
    irccon.SASLPassword = creds.Password
}

// Main
func main() {
    irccon := irc.IRC(botname, botident)
    irccon.VerboseCallbackHandler = true
    irccon.Debug = false
    irccon.UseTLS = UseTLS
    irccon.TLSConfig = &tls.Config{
        ServerName: servername,
        InsecureSkipVerify: false,
    }
    irccon.UseSASL = UseSASL
    if UseSASL {
        irccon.SASLMech = SASLMech
        setupSASL(irccon)
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

    // Do nothing (?) at the end of the nickname list
    irccon.AddCallback("366", func(e *irc.Event) {  })

    // Handle joins
    irccon.AddCallback("JOIN", func(e *irc.Event) {
        // If the bot is the one joining
        if e.Nick == irccon.GetNick() {
            // e.Arguments[0] contains the channel name
            irccon.Privmsg(e.Arguments[0], fmt.Sprintf("%s", irccon.GetNick()))
        }
    })

    // Handle messages
    irccon.AddCallback("PRIVMSG", func(e *irc.Event) {
        
        eChannel := e.Arguments[0]
        eMessage := e.Message()

        // Skip messages that aren't from a channel
        if !strings.HasPrefix(eChannel, "#") {
            return
        }

        // Log user activity for .seen command
        _, err := db.Exec(`
            INSERT INTO user_activity (nickname, channel, last_message, last_seen)
            VALUES (?, ?, ?, CURRENT_TIMESTAMP)
            ON CONFLICT(nickname, channel) DO UPDATE SET
                last_message = excluded.last_message,
                last_seen = excluded.last_seen
        `, strings.ToLower(e.Nick), eChannel, eMessage)
        if err != nil {
            log.Printf("Error updating user activity: %v", err)
        }

        // Don't respond to itself
        if e.Nick == irccon.GetNick() {
            return
        }

        // Reply to mentions
        if strings.Contains(strings.ToLower(eMessage), strings.ToLower(irccon.GetNick())) {
            irccon.Privmsg(eChannel, irccon.GetNick())
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
        `, strings.ToLower(e.Nick), eChannel)
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
                irccon.Privmsg(eChannel, 
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

        msg := strings.TrimSpace(eMessage)
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