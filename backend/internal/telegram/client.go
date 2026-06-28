package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

type Client struct {
	token  string
	client *http.Client
}

type SendMessageRequest struct {
	ChatID      int64           `json:"chat_id"`
	Text        string          `json:"text"`
	ParseMode   string          `json:"parse_mode,omitempty"`
	ReplyMarkup json.RawMessage `json:"reply_markup,omitempty"`
}

type SetWebhookRequest struct {
	URL string `json:"url"`
}

type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	Text string `json:"text"`
	From User   `json:"from"`
	Chat Chat   `json:"chat"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type CallbackQuery struct {
	ID      string  `json:"id"`
	From    User    `json:"from"`
	Message Message `json:"message"`
	Data    string  `json:"data"`
}

type User struct {
	ID        int64   `json:"id"`
	Username  *string `json:"username"`
	FirstName string  `json:"first_name"`
	LastName  *string `json:"last_name"`
}

type APIResponse struct {
	Ok          bool            `json:"ok"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

func NewClient(token string) *Client {
	return &Client{
		token: token,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) BaseURL() string {
	return fmt.Sprintf("https://api.telegram.org/bot%s", c.token)
}

func (c *Client) SendMessage(chatID int64, text string) error {
	return c.SendMessageWithMarkup(chatID, text, nil)
}

func (c *Client) SendMessageWithMarkup(chatID int64, text string, replyMarkup json.RawMessage) error {
	body := SendMessageRequest{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: replyMarkup,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.client.Post(c.BaseURL()+"/sendMessage", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if !apiResp.Ok {
		return fmt.Errorf("telegram api error: %s", apiResp.Description)
	}

	return nil
}

func (c *Client) SendPhoto(chatID int64, photo []byte, caption string) error {
	return c.SendPhotoWithMarkup(chatID, photo, caption, nil)
}

func (c *Client) SendPhotoWithMarkup(chatID int64, photo []byte, caption string, replyMarkup json.RawMessage) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return fmt.Errorf("write chat id: %w", err)
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return fmt.Errorf("write caption: %w", err)
		}
		if err := writer.WriteField("parse_mode", "HTML"); err != nil {
			return fmt.Errorf("write parse mode: %w", err)
		}
	}
	if len(replyMarkup) > 0 {
		if err := writer.WriteField("reply_markup", string(replyMarkup)); err != nil {
			return fmt.Errorf("write reply markup: %w", err)
		}
	}

	part, err := writer.CreateFormFile("photo", "price-block.png")
	if err != nil {
		return fmt.Errorf("create photo form: %w", err)
	}
	if _, err := part.Write(photo); err != nil {
		return fmt.Errorf("write photo: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequest("POST", c.BaseURL()+"/sendPhoto", &body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("send photo: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if !apiResp.Ok {
		return fmt.Errorf("telegram api error: %s", apiResp.Description)
	}

	return nil
}

func (c *Client) AnswerCallbackQuery(callbackID string) error {
	body := map[string]string{"callback_query_id": callbackID}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal callback answer: %w", err)
	}

	resp, err := c.client.Post(c.BaseURL()+"/answerCallbackQuery", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("answer callback: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if !apiResp.Ok {
		return fmt.Errorf("telegram api error: %s", apiResp.Description)
	}

	return nil
}

func (c *Client) SetWebhook(url string) error {
	body := SetWebhookRequest{URL: url}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal webhook: %w", err)
	}

	resp, err := c.client.Post(c.BaseURL()+"/setWebhook", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("set webhook: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if !apiResp.Ok {
		return fmt.Errorf("set webhook failed: %s", apiResp.Description)
	}

	return nil
}

func (c *Client) GetMe() (*User, error) {
	resp, err := c.client.Get(c.BaseURL() + "/getMe")
	if err != nil {
		return nil, fmt.Errorf("get me: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if !apiResp.Ok {
		return nil, fmt.Errorf("get me failed: %s", apiResp.Description)
	}

	var user User
	if err := json.Unmarshal(apiResp.Result, &user); err != nil {
		return nil, fmt.Errorf("parse user: %w", err)
	}

	return &user, nil
}
