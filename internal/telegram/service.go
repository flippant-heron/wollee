package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	appservice "github.com/flippant-heron/wollee/internal/service"
)

type Handler interface {
	List() string
	Wake(target string) string
}

type Service struct {
	token   string
	allowed map[int64]struct{}
	handler Handler
	logger  *appservice.Logger
	bot     *tgbotapi.BotAPI
}

func New(token string, allowedUsers []int64, handler Handler, logger *appservice.Logger) *Service {
	allowed := make(map[int64]struct{}, len(allowedUsers))
	for _, userID := range allowedUsers {
		allowed[userID] = struct{}{}
	}
	return &Service{token: token, allowed: allowed, handler: handler, logger: logger}
}

func (s *Service) Enabled() bool {
	return strings.TrimSpace(s.token) != ""
}

func (s *Service) Start(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	if s.handler == nil {
		return errors.New("telegram handler is required")
	}

	bot, err := tgbotapi.NewBotAPI(s.token)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}
	s.bot = bot

	updates := bot.GetUpdatesChan(tgbotapi.NewUpdate(0))
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok || update.Message == nil {
					continue
				}
				s.handleMessage(update.Message)
			}
		}
	}()

	return nil
}

func (s *Service) Shutdown() {
	if s.bot != nil {
		s.bot.StopReceivingUpdates()
	}
}

func (s *Service) handleMessage(message *tgbotapi.Message) {
	if message.From == nil {
		return
	}
	if _, ok := s.allowed[message.From.ID]; !ok {
		s.reply(message.Chat.ID, "unauthorized")
		return
	}

	switch strings.ToLower(message.Command()) {
	case "list":
		s.reply(message.Chat.ID, s.handler.List())
	case "wake":
		target := strings.TrimSpace(message.CommandArguments())
		if target == "" {
			s.reply(message.Chat.ID, "Usage: /wake <hostname|mac>")
			return
		}
		s.reply(message.Chat.ID, s.handler.Wake(target))
	default:
		s.reply(message.Chat.ID, "Supported commands: /list, /wake <hostname|mac>")
	}
}

func (s *Service) reply(chatID int64, text string) {
	if s.bot == nil {
		return
	}
	if _, err := s.bot.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		s.logger.Error("send telegram reply", err, "chat_id", chatID)
	}
}
