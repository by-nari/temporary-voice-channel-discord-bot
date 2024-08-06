package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state"
)

var token = os.Getenv("BOT_TOKEN")

func main() {
	if token == "" {
		log.Fatalln("No $BOT_TOKEN given.")
	}

	// Initialize the state
	s := state.New("Bot " + token)

	// Add intents
	s.AddIntents(gateway.IntentGuildVoiceStates)

	// Create a new handler
	h := newHandler(s)

	// Register the handler
	s.AddHandler(h.onReady)
	s.AddHandler(h.onVoiceStateUpdate)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := s.Open(ctx); err != nil {
		log.Fatalln("cannot connect:", err)
	}

	<-ctx.Done()

	if err := s.Close(); err != nil {
		log.Printf("Failed to gracefully close session: %v", err)
	}
}

type handler struct {
	s                   *state.State
	mu                  sync.Mutex
	userVoiceStates     map[discord.UserID]discord.VoiceState
	temporaryChannels   []discord.ChannelID
	temporaryCategories []discord.ChannelID
}

func newHandler(s *state.State) *handler {
	return &handler{
		s:               s,
		userVoiceStates: make(map[discord.UserID]discord.VoiceState),
	}
}

// onReady is called when the bot is ready
func (h *handler) onReady(e *gateway.ReadyEvent) {
	me, _ := h.s.Me()
	log.Println("connected to the gateway as", me.Username)
}

// onVoiceStateUpdate handles voice state updates
func (h *handler) onVoiceStateUpdate(evt *gateway.VoiceStateUpdateEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Get the previous state if it exists
	before := h.userVoiceStates[evt.UserID]
	// Update to the new state
	h.userVoiceStates[evt.UserID] = evt.VoiceState

	possibleChannelName := evt.Member.User.Username + "'s room"

	fmt.Printf("User %s changed voice channel from %s to %s\n", evt.UserID, before.ChannelID, evt.ChannelID)

	if before.ChannelID.String() == "" && evt.ChannelID.IsValid() {
		// User joined a channel
		if before.ChannelID != evt.ChannelID {
			afterChannel, err := h.s.Channel(evt.ChannelID)
			if err != nil {
				log.Println("Failed to get after channel:", err)
				return
			}

			if afterChannel.Name == "🐕 bark" {
				tempChannel, err := h.s.CreateChannel(afterChannel.GuildID, api.CreateChannelData{
					Name:       possibleChannelName,
					Type:       discord.GuildVoice,
					CategoryID: afterChannel.ParentID,
				})
				if err != nil {
					log.Println("Failed to clone channel:", err)
					return
				}
				err = h.s.ModifyMember(afterChannel.GuildID, evt.UserID, api.ModifyMemberData{
					VoiceChannel: tempChannel.ID,
				})
				if err != nil {
					log.Println("Failed to move member:", err)
					return
				}
				h.temporaryChannels = append(h.temporaryChannels, tempChannel.ID)
			}

			if afterChannel.Name == "teams" {
				temporaryCategory, err := h.s.CreateChannel(afterChannel.GuildID, api.CreateChannelData{
					Name: possibleChannelName,
					Type: discord.GuildCategory,
				})
				if err != nil {
					log.Println("Failed to create category:", err)
					return
				}

				_, err = h.s.CreateChannel(temporaryCategory.GuildID, api.CreateChannelData{
					Name:       "text",
					Type:       discord.GuildText,
					CategoryID: temporaryCategory.ID,
				})
				if err != nil {
					log.Println("Failed to create text channel:", err)
					return
				}

				tempChannel, err := h.s.CreateChannel(temporaryCategory.GuildID, api.CreateChannelData{
					Name:       "voice",
					Type:       discord.GuildVoice,
					CategoryID: temporaryCategory.ID,
				})
				if err != nil {
					log.Println("Failed to create voice channel:", err)
					return
				}

				err = h.s.ModifyMember(temporaryCategory.GuildID, evt.UserID, api.ModifyMemberData{
					VoiceChannel: tempChannel.ID,
				})
				if err != nil {
					log.Println("Failed to move member:", err)
					return
				}

				h.temporaryCategories = append(h.temporaryCategories, tempChannel.ID)
			}
		}
	}

	if before.ChannelID.IsValid() && evt.ChannelID.String() == "" {
		// User left a channel
		beforeChannel, err := h.s.Channel(before.ChannelID)
		if err != nil {
			log.Println("Failed to get before channel:", err)
			return
		}

		if contains(h.temporaryChannels, beforeChannel.ID) {
			if len(beforeChannel.DMRecipients) == 0 {
				err := h.s.DeleteChannel(beforeChannel.ID, "cleaning up")
				if err != nil {
					log.Println("Failed to delete channel:", err)
				}
				remove(&h.temporaryChannels, beforeChannel.ID)
			}
		}

		categoryID := beforeChannel.ParentID
		if categoryID != 0 && contains(h.temporaryCategories, beforeChannel.ID) {
			category, err := h.s.Channel(categoryID)
			if err == nil && len(beforeChannel.DMRecipients) == 0 {
				channels, err := h.s.Channels(category.GuildID)
				if err != nil {
					log.Println("Failed to fetch channels:", err)
					return
				}
				for _, channel := range channels {
					if channel.ParentID == categoryID {
						_ = h.s.DeleteChannel(channel.ID, "cleaning up")
					}
				}
				err = h.s.DeleteChannel(category.ID, "cleaning up")
				if err != nil {
					log.Println("Failed to delete category:", err)
				}
				remove(&h.temporaryCategories, categoryID)
			}
		}
	}
}

func contains(slice []discord.ChannelID, elem discord.ChannelID) bool {
	for _, item := range slice {
		if item == elem {
			return true
		}
	}
	return false
}

func remove(slice *[]discord.ChannelID, elem discord.ChannelID) {
	for i, item := range *slice {
		if item == elem {
			*slice = append((*slice)[:i], (*slice)[i+1:]...)
			break
		}
	}
}
