package telegram

import (
	"context"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"trackway/internal/util"
)

const maxMessageLength = 4000

type UpdateHandler func(ctx context.Context, update *models.Update)

type Client struct {
	bot    *tgbot.Bot
	chatID int64
}

func New(token string, chatID int64, handler UpdateHandler) (*Client, error) {
	b, err := tgbot.New(
		token,
		tgbot.WithDefaultHandler(func(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
			handler(ctx, update)
		}),
		tgbot.WithNotAsyncHandlers(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{bot: b, chatID: chatID}, nil
}

func (c *Client) Start(ctx context.Context) {
	c.bot.Start(ctx)
}

func (c *Client) SendDefaultHTML(ctx context.Context, text string) error {
	return c.SendHTML(ctx, c.chatID, text)
}

func (c *Client) SendHTML(ctx context.Context, chatID int64, text string) error {
	for _, chunk := range util.SplitByLimit(text, maxMessageLength) {
		_, err := c.bot.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:    chatID,
			Text:      chunk,
			ParseMode: models.ParseModeHTML,
		})
		if err != nil {
			return err
		}
	}
	return nil
}
