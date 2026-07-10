package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/littlewell/price-tracker/internal/telegram"
)

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type inlineKeyboard struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

func makeInlineKeyboard(rows ...[]inlineButton) json.RawMessage {
	data, _ := json.Marshal(inlineKeyboard{InlineKeyboard: rows})
	return data
}

func button(text, data string) inlineButton {
	return inlineButton{Text: text, CallbackData: data}
}

// replyButton is a key on a reply keyboard — the kind pinned under the input field.
// Unlike an inline button it carries no callback data: tapping it sends its Text back as
// an ordinary message, which the update handler matches with isButtonLabel.
type replyButton struct {
	Text string `json:"text"`
}

type replyKeyboard struct {
	Keyboard       [][]replyButton `json:"keyboard"`
	ResizeKeyboard bool            `json:"resize_keyboard"`
	IsPersistent   bool            `json:"is_persistent"`
}

func replyBtn(text string) replyButton {
	return replyButton{Text: text}
}

// makeReplyKeyboard builds a keyboard pinned under the input field. resize_keyboard keeps
// it compact; is_persistent keeps it always visible instead of collapsing to the keyboard
// icon. Deliberately no one_time_keyboard — that would hide it after the first tap, which
// is the opposite of what we want here.
func makeReplyKeyboard(rows ...[]replyButton) json.RawMessage {
	data, _ := json.Marshal(replyKeyboard{
		Keyboard:       rows,
		ResizeKeyboard: true,
		IsPersistent:   true,
	})
	return data
}

// mainMenuReplyKeyboard is the persistent 2x2 keyboard shown under the input field at every
// "home" point (language chosen, back to menu, ...). Its taps are routed by label in the
// update handler's message switch, not by callback data.
func mainMenuReplyKeyboard(lang string) json.RawMessage {
	return makeReplyKeyboard(
		[]replyButton{replyBtn(tr(lang, "button_new_tracker")), replyBtn(tr(lang, "button_trackers"))},
		[]replyButton{replyBtn(tr(lang, "button_success_history")), replyBtn(tr(lang, "button_plans"))},
		[]replyButton{replyBtn(tr(lang, "button_instruction"))},
	)
}

func linkButton(text, url string) inlineButton {
	return inlineButton{Text: text, URL: url}
}

func sendLanguageMenu(tg *telegram.Client, chatID int64) {
	markup := makeInlineKeyboard(
		[]inlineButton{button("English", "lang:en")},
		[]inlineButton{button("Русский", "lang:ru"), button("Polski", "lang:pl")},
	)
	_ = tg.SendMessageWithMarkup(chatID, tr(defaultBotLanguage, "choose_language"), markup)
}

// sendMainMenu shows the persistent reply keyboard (New tracker / My trackers / Pricing /
// Instruction) pinned under the input field. Language is reached via /lang rather than a
// menu button now that the four primary actions live on the always-visible keyboard.
func sendMainMenu(tg *telegram.Client, chatID int64, lang, text string) {
	_ = tg.SendMessageWithMarkup(chatID, text, mainMenuReplyKeyboard(lang))
}

// tributeSubscriptionURL maps a paid plan code to its Tribute Mini App subscription
// link (opens Telegram's built-in Tribute app to the specific product/tier).
var tributeSubscriptionURL = map[string]string{
	"basic": "https://t.me/tribute/app?startapp=sZIM",
	"pro":   "https://t.me/tribute/app?startapp=sZIL",
}

func sendPlansMenu(ctx context.Context, pool *pgxpool.Pool, tg *telegram.Client, chatID int64, userID, lang string) {
	currentPlan := getPlanLimits(ctx, pool, userID).code
	trialUsed := hasUsedTrial(ctx, pool, userID)

	plans := []struct {
		code, nameKey, taglineKey, trackersKey, frequencyKey string
	}{
		{"free", "plan_free_name", "plan_free_tagline", "plan_free_trackers", ""},
		{"trial", "plan_trial_name", "plan_trial_tagline", "plan_trial_trackers", "plan_basic_frequency"},
		{"basic", "plan_basic_name", "plan_basic_tagline", "plan_basic_trackers", "plan_basic_frequency"},
		{"pro", "plan_pro_name", "plan_pro_tagline", "plan_pro_trackers", "plan_pro_frequency"},
	}
	for i, p := range plans {
		text := fmt.Sprintf("💳 <b>%s</b>\n<i>%s</i>\n\n📦 %s",
			tr(lang, p.nameKey), tr(lang, p.taglineKey), tr(lang, p.trackersKey))
		if p.frequencyKey != "" {
			text += "\n⚡ " + tr(lang, p.frequencyKey)
		}

		var rows [][]inlineButton
		switch {
		case p.code == currentPlan:
			// Mark the active plan and omit its "Activate" button — nothing to activate.
			text += "\n\n<b>" + tr(lang, "plan_current") + "</b>"
		case p.code == "free":
			// Free is the default everyone always has — nothing to activate, ever.
		case p.code == "trial" && trialUsed:
			text += "\n\n" + tr(lang, "plan_trial_used")
		default:
			activateBtn := button(tr(lang, "button_activate"), "plan:"+p.code)
			if url, ok := tributeSubscriptionURL[p.code]; ok {
				activateBtn = linkButton(tr(lang, "button_activate"), url)
			} else if p.code == "trial" {
				activateBtn = button(tr(lang, "button_activate"), "activate_trial")
			}
			rows = append(rows, []inlineButton{activateBtn})
		}
		if i == len(plans)-1 {
			rows = append(rows, []inlineButton{button(tr(lang, "button_back"), "menu:back")})
		}

		var markup json.RawMessage
		if len(rows) > 0 {
			markup = makeInlineKeyboard(rows...)
		}
		_ = tg.SendMessageWithMarkup(chatID, text, markup)
	}
}

func sendBackMessage(tg *telegram.Client, chatID int64, lang, text string) {
	markup := makeInlineKeyboard([]inlineButton{button(tr(lang, "button_back"), "menu:back")})
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}

func sendModeMenu(tg *telegram.Client, chatID int64, lang, text string) {
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_track_price"), "mode:price"), button(tr(lang, "button_track_stock"), "mode:stock")},
		[]inlineButton{button(tr(lang, "button_back"), "menu:back")},
	)
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
}
