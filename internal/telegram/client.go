package telegram

import (
	"context"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"trackway/internal/util"
)

const maxMessageLength = 4000
const sendTimeout = 10 * time.Second

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
	for _, chunk := range util.SplitByLineLimit(text, maxMessageLength) {
		chunkCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		_, err := c.bot.SendMessage(chunkCtx, &tgbot.SendMessageParams{
			ChatID:    chatID,
			Text:      chunk,
			ParseMode: models.ParseModeHTML,
		})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
