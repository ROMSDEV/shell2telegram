package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Syfaro/telegram-bot-api"
)

// version
const VERSION = "1.0"

// bot default timeout
const DEFAULT_BOT_TIMEOUT = 60

// Commands - one command type
type Commands map[string]string

// Config - config struct
type Config struct {
	token       string   // bot token
	addExit     bool     // adding /shell2telegram exit command
	botTimeout  int      // bot timeout
	allowUsers  []string // telegram users who are allowed to chat with the bot
	rootUsers   []string // telegram users, who confirms new users in their private chat
	allowAll    bool     // allow all user (DANGEROUS!)
	logCommands bool     // logging all commands
}

// ----------------------------------------------------------------------------
// get config
func getConfig() (commands Commands, appConfig Config, err error) {
	flag.StringVar(&appConfig.token, "tb-token", "", "setting bot token (or set TB_TOKEN variable)")
	flag.BoolVar(&appConfig.addExit, "add-exit", false, "adding \"/shell2telegram exit\" command for terminate bot (for roots only)")
	flag.IntVar(&appConfig.botTimeout, "timeout", DEFAULT_BOT_TIMEOUT, "setting timeout for bot")
	flag.BoolVar(&appConfig.allowAll, "allow-all", false, "allow all users (DANGEROUS!)")
	flag.BoolVar(&appConfig.logCommands, "log-commands", false, "logging all commands")
	logFilename := flag.String("log", "", "log filename, default - STDOUT")
	allowUsers := flag.String("allow-users", "", "telegram users who are allowed to chat with the bot (\"user1,user2\")")
	rootUsers := flag.String("root-users", "", "telegram users, who confirms new users in their private chat (\"user1,user2\")")
	version := flag.Bool("version", false, "get version")

	flag.Usage = func() {
		fmt.Printf("usage: %s [options] /chat_command \"shell command\" /chat_command2 \"shell command2\"\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}
	flag.Parse()

	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	// setup log file
	if len(*logFilename) > 0 {
		fhLog, err := os.OpenFile(*logFilename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("error opening log file: %v", err)
		}
		log.SetOutput(fhLog)
	}

	// setup users and roots
	if *allowUsers != "" {
		appConfig.allowUsers = strings.Split(*allowUsers, ",")
	}
	if *rootUsers != "" {
		appConfig.rootUsers = strings.Split(*rootUsers, ",")
	}

	commands = Commands{}
	// need >= 2 arguments and count of it must be even
	args := flag.Args()
	if len(args) < 2 || len(args)%2 == 1 {
		return commands, appConfig, fmt.Errorf("error: need pairs of chat-command and shell-command")
	}

	for i := 0; i < len(args); i += 2 {
		path, cmd := args[i], args[i+1]
		if path[0] != '/' {
			return commands, appConfig, fmt.Errorf("error: path %s dont starts with /", path)
		}
		commands[path] = cmd
	}

	if appConfig.token == "" {
		if appConfig.token = os.Getenv("TB_TOKEN"); appConfig.token == "" {
			return commands, appConfig, fmt.Errorf("TB_TOKEN environment var not found. See https://core.telegram.org/bots#botfather for more information\n")
		}
	}

	return commands, appConfig, nil
}

// ----------------------------------------------------------------------------
func sendMessageWithLogging(bot *tgbotapi.BotAPI, chatID int, replayMsg string) {
	_, err := bot.SendMessage(tgbotapi.NewMessage(chatID, replayMsg))
	if err != nil {
		log.Print("Bot send message error: ", err)
	}
}

// ----------------------------------------------------------------------------
func main() {
	commands, appConfig, err := getConfig()
	if err != nil {
		log.Fatal(err)
	}

	bot, err := tgbotapi.NewBotAPI(appConfig.token)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Authorized on bot account: %s", bot.Self.UserName)

	tgbotConfig := tgbotapi.NewUpdate(0)
	tgbotConfig.Timeout = appConfig.botTimeout
	err = bot.UpdatesChan(tgbotConfig)
	if err != nil {
		log.Fatal(err)
	}

	doExit := false
	users := NewUsers(appConfig)
	vacuumTicker := time.Tick(SECONDS_FOR_OLD_USERS_BEFORE_VACUUM * time.Second)

LOOP:
	for {
		select {
		case telegramUpdate := <-bot.Updates:

			messageText := regexp.MustCompile(`\s+`).Split(telegramUpdate.Message.Text, 2)
			messageCmd, messageArgs := messageText[0], ""
			if len(messageText) > 1 {
				messageArgs = messageText[1]
			}

			replayMsg := ""

			if len(messageText) > 0 && len(messageCmd) > 0 && messageCmd[0] == '/' {

				userID := telegramUpdate.Message.From.ID

				users.AddNew(telegramUpdate.Message)
				allowExec := appConfig.allowAll || users.IsAuthorized(userID)
				ctx := Ctx{
					bot:         bot,
					appConfig:   appConfig,
					commands:    commands,
					users:       users,
					userID:      userID,
					allowExec:   allowExec,
					messageCmd:  messageCmd,
					messageArgs: messageArgs,
				}

				switch {
				// commands .................................
				case messageCmd == "/auth" || messageCmd == "/authroot":
					replayMsg = cmdAuth(ctx)

				case messageCmd == "/help":
					replayMsg = cmdHelp(ctx)

				case messageCmd == "/shell2telegram" && messageArgs == "stat" && users.IsRoot(userID):
					replayMsg = cmdShell2telegramStat(ctx)

				case messageCmd == "/shell2telegram" && messageArgs == "exit" && users.IsRoot(userID) && appConfig.addExit:
					replayMsg = "bye..."
					doExit = true

				case messageCmd == "/shell2telegram" && messageArgs == "version":
					replayMsg = fmt.Sprintf("shell2telegram %s", VERSION)

				case allowExec:
					replayMsg = cmdUser(ctx)
				} // switch for commands

				if replayMsg != "" {
					sendMessageWithLogging(bot, telegramUpdate.Message.Chat.ID, replayMsg)
					if appConfig.logCommands {
						log.Printf("%d @%s: %s", userID, telegramUpdate.Message.From.UserName, telegramUpdate.Message.Text)
					}

					if doExit {
						break LOOP
					}
				}
			}

		case <-vacuumTicker:
			users.clearOldUsers()
		}
	}
}
