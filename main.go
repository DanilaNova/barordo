package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/api/cmdroute"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
	"github.com/diamondburned/arikawa/v3/utils/json/option"
)

var commands = []api.CreateCommandData{
	{
		Name:        "ping",
		Description: "ping pong!",
	},
	{
		Name:        "echo",
		Description: "echo back the argument",
		Options: []discord.CommandOption{
			&discord.StringOption{
				OptionName:  "argument",
				Description: "what's echoed back",
				Required:    true,
			},
		},
	},
	{
		Name:        "thonk",
		Description: "biiiig thonk",
	},
}

type PresenceDataKeyType struct{}

var PresenceDataKey = PresenceDataKeyType{}
var PresenceUpdatePeriod = time.Duration(5_000_000_000) // 5 seconds (4 updates per 20 seconds)
var PresenceCheckPeriod = time.Duration(1_000_000_000)  // 1 second

func main() {
	date := time.Now().Format(time.DateOnly)

	_, err := os.ReadDir("./logs")
	if err != nil {
		err = os.Mkdir("./logs", 0644)
	}

	if err != nil {
		log.Println("[WARN]: cannot create logs directory: err")

	} else {
		logFile, err := os.OpenFile("./logs/"+date+".txt", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Println("[WARN]: cannot open log file: ", err)
		} else {
			forkWriter := ForkWriter{outputs: []io.Writer{log.Writer(), logFile}}
			log.SetOutput(&forkWriter)
			log.Println("set a fork for a log file")
			defer logFile.Close()
		}
	}

	log.Println("START")

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatalln("No $BOT_TOKEN given.")
	}

	h := newHandler(state.New("Bot " + token))
	h.s.AddInteractionHandler(h)
	h.s.AddIntents(gateway.IntentGuilds)
	h.s.AddIntents(gateway.IntentGuildPresences)

	h.s.AddHandler(func(*gateway.ReadyEvent) {
		me, _ := h.s.Me()
		log.Println("connected to the gateway as", me.Tag())
	})

	if err := cmdroute.OverwriteCommands(h.s, commands); err != nil {
		log.Fatalln("cannot update commands:", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	ctx = context.WithValue(ctx, PresenceDataKey, &PresenceData{})
	context.AfterFunc(ctx, func() {
		log.Println("STOP")
	})
	defer cancel()

	http.HandleFunc("GET /status", httpStatusGet(h.s))
	http.HandleFunc("POST /status", httpStatusPost(ctx, h.s))

	go func(ctx context.Context) {
		log.Println("presence updater is up")
		for {
			select {
			case <-ctx.Done():
				{
					return
				}
			default:
				value := ctx.Value(PresenceDataKey).(*PresenceData)

				UpdatePresence(h.s, ctx, value)

				time.Sleep(PresenceCheckPeriod)
			}
		}

	}(ctx)

	go func(ctx context.Context) {
		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Fatalln("cannot create http server:", err)
		}
	}(ctx)
	log.Println("HTTP server is up")

	if err := h.s.Connect(ctx); err != nil {
		log.Fatalln("cannot connect:", err)
	}
}

type handler struct {
	*cmdroute.Router
	s *state.State
}

func newHandler(s *state.State) *handler {
	h := &handler{s: s}

	h.Router = cmdroute.NewRouter()
	// Automatically defer handles if they're slow.
	h.Use(cmdroute.Deferrable(s, cmdroute.DeferOpts{}))
	h.AddFunc("ping", h.cmdPing)
	h.AddFunc("echo", h.cmdEcho)
	h.AddFunc("thonk", h.cmdThonk)

	return h
}

// Discord bot

func (h *handler) cmdPing(ctx context.Context, cmd cmdroute.CommandData) *api.InteractionResponseData {
	log.Printf("%s(%s) pinged\n", cmd.Event.Sender().Username, cmd.Event.SenderID())
	return &api.InteractionResponseData{
		Content: option.NewNullableString("Pong!"),
	}
}

func (h *handler) cmdEcho(ctx context.Context, cmd cmdroute.CommandData) *api.InteractionResponseData {
	var options struct {
		Arg string `discord:"argument"`
	}

	if err := cmd.Options.Unmarshal(&options); err != nil {
		log.Println("error while decoding echo options: ", err)
		return errorResponse(err)
	}

	log.Printf("%s(%s) echoed: %q\n", cmd.Event.Sender().Username, cmd.Event.SenderID(), options.Arg)
	return &api.InteractionResponseData{
		Content:         option.NewNullableString(options.Arg),
		AllowedMentions: &api.AllowedMentions{}, // don't mention anyone
	}
}

func (h *handler) cmdThonk(ctx context.Context, data cmdroute.CommandData) *api.InteractionResponseData {
	time.Sleep(time.Duration(3+rand.Intn(5)) * time.Second)
	log.Println("thonkd")
	return &api.InteractionResponseData{
		Content: option.NewNullableString("https://tenor.com/view/thonk-thinking-sun-thonk-sun-thinking-sun-gif-14999983"),
	}
}

func errorResponse(err error) *api.InteractionResponseData {
	return &api.InteractionResponseData{
		Content:         option.NewNullableString("**Error:** " + err.Error()),
		Flags:           discord.EphemeralMessage,
		AllowedMentions: &api.AllowedMentions{ /* none */ },
	}
}

// HTTP server

func httpStatusGet(s *state.State) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		var stage strings.Builder
		stage.WriteString("status.get")
		publicOutput := make([]string, 0)
		privateOutput := make([]string, 0)

		log.Println("requested current presence")

		user, err := s.Me()
		if err != nil {
			stage.WriteString(".user_id")
			writeError(w, http.StatusInternalServerError, stage, err, publicOutput, privateOutput)
			return
		}

		presence, err := s.Presence(discord.NullGuildID, user.ID)
		if err != nil {
			stage.WriteString(".presence")
			writeError(w, http.StatusInternalServerError, stage, err, publicOutput, privateOutput)
			return
		}

		body, _ := json.Marshal(presence)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}

func httpStatusPost(ctx context.Context, s *state.State) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		w.Header().Add("Content-Type", "application/json")
		var stage strings.Builder
		stage.WriteString("status.post")
		publicOutput := make([]string, 0)
		privateOutput := make([]string, 0)

		log.Println("requested switch presence")

		buffer := make([]byte, 4096)
		count, _ := r.Body.Read(buffer)
		buffer = buffer[:count]

		presenceData := ctx.Value(PresenceDataKey).(*PresenceData)
		accumulatedPresence := &presenceData.AccumulatedPresence

		if !presenceData.HasValue {
			*accumulatedPresence = gateway.UpdatePresenceCommand{}
		}

		err := json.Unmarshal(buffer, accumulatedPresence)
		if err != nil {
			publicOutput = append(publicOutput, string(buffer))
			stage.WriteString(".decoding")
			writeError(w, http.StatusUnprocessableEntity, stage, err, publicOutput, privateOutput)
			return
		}

		presenceData.HasValue = true

		inQueue, err := UpdatePresence(s, ctx, presenceData)

		if err != nil {
			str, _ := json.Marshal(*accumulatedPresence)
			publicOutput = append(publicOutput, string(str))
			stage.WriteString(".sending")
			writeError(w, http.StatusUnprocessableEntity, stage, err, publicOutput, privateOutput)
			return
		}

		switch inQueue {
		case false:
			{
				w.WriteHeader(http.StatusOK)
			}
		case true:
			{
				w.WriteHeader(http.StatusAccepted)
				log.Printf("presence in queue: %s\n", string(JsonMarshalUnwrap(accumulatedPresence)))
			}
		}

	}
}

func createErrorBody(stage, err string, additional ...string) []byte {
	obj := map[string]string{"stage": stage, "error": err}
	for i, v := range additional {
		obj[strconv.Itoa(i)] = v
	}
	body, _ := json.Marshal(obj)
	return body
}

type LogEntry struct {
	str  string
	send bool
}

func writeError(w http.ResponseWriter, status int, stage strings.Builder, error error, additionalPublic, additionalPrivate []string) {
	logOutput := make([]string, 0, len(additionalPublic)+len(additionalPrivate))
	logOutput = append(logOutput, additionalPublic...)
	logOutput = append(logOutput, additionalPrivate...)

	w.WriteHeader(status)
	w.Write(createErrorBody(stage.String(), error.Error(), additionalPublic...))
	log.Printf("error when processing http request\n\tstatus: %d\n\tstage: %s\n\terror: %s\n\tadditional info: %q", status, stage.String(), error, logOutput)
}

type ForkWriter struct {
	outputs []io.Writer
}

func (self *ForkWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	for _, output := range self.outputs {
		lastN, lastErr := output.Write(p)
		n = min(n, lastN)
		if err == nil && lastErr != nil {
			err = lastErr
		}
	}
	return
}

type PresenceData struct {
	LastUpdate          time.Time
	AccumulatedPresence gateway.UpdatePresenceCommand
	HasValue            bool
}

func UpdatePresence(s *state.State, ctx context.Context, presence *PresenceData) (inQueue bool, err error) {
	if !presence.HasValue {
		return false, nil
	}
	if time.Since(presence.LastUpdate) < PresenceUpdatePeriod {
		return true, nil
	}

	err = s.Gateway().Send(ctx, &presence.AccumulatedPresence)
	if err != nil {
		return true, err
	}

	presence.HasValue = false
	presence.LastUpdate = time.Now()
	log.Printf("presence switched to: %s\n", string(JsonMarshalUnwrap(presence.AccumulatedPresence)))
	return false, nil
}

func JsonMarshalUnwrap(v any) []byte {
	jsonData, _ := json.Marshal(v)
	return jsonData
}
