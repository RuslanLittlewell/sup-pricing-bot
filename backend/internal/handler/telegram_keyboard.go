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

func sendMainMenu(tg *telegram.Client, chatID int64, lang, text string) {
	markup := makeInlineKeyboard(
		[]inlineButton{button(tr(lang, "button_new_tracker"), "menu:new")},
		[]inlineButton{button(tr(lang, "button_trackers"), "menu:list")},
		[]inlineButton{button(tr(lang, "button_plans"), "menu:plans")},
		[]inlineButton{button(tr(lang, "button_choose_language"), "menu:language")},
	)
	_ = tg.SendMessageWithMarkup(chatID, text, markup)
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
		code, nameKey, taglineKey, trackersKey, intervalKey string
	}{
		{"free", "plan_free_name", "plan_free_tagline", "plan_free_trackers", "plan_free_interval"},
		{"trial", "plan_trial_name", "plan_trial_tagline", "plan_trial_trackers", "plan_trial_interval"},
		{"basic", "plan_basic_name", "plan_basic_tagline", "plan_basic_trackers", "plan_basic_interval"},
		{"pro", "plan_pro_name", "plan_pro_tagline", "plan_pro_trackers", "plan_pro_interval"},
	}
	for i, p := range plans {
		text := fmt.Sprintf("💳 <b>%s</b>\n<i>%s</i>\n\n📦 %s\n⏱ %s",
			tr(lang, p.nameKey), tr(lang, p.taglineKey), tr(lang, p.trackersKey), tr(lang, p.intervalKey))

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

// intervalOptions are the selectable scan frequencies (minutes), ascending. Only those
// >= the user's plan minimum are shown, so e.g. Free never sees the 30m/1h buttons — the
// 5-minute option in particular only ever shows up for the internal 'admin' plan (see
// migrationV19), no paid tier goes that fast.
var intervalOptions = []int{5, 30, 60, 180, 300, 1440}

func sendIntervalMenu(tg *telegram.Client, chatID int64, lang, trackerID, text string, minInterval int) {
	var row []inlineButton
	var rows [][]inlineButton
	for _, m := range intervalOptions {
		if m < minInterval {
			continue
		}
		row = append(row, button(intervalOptionLabel(lang, m), fmt.Sprintf("interval:%s:%d", trackerID, m)))
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []inlineButton{button(tr(lang, "button_back"), "menu:list")})
	_ = tg.SendMessageWithMarkup(chatID, text, makeInlineKeyboard(rows...))
}

func intervalOptionLabel(lang string, minutes int) string {
	if minutes%60 == 0 {
		hours := minutes / 60
		if lang == "ru" {
			return fmt.Sprintf("%dч", hours)
		}
		return fmt.Sprintf("%dh", hours)
	}
	if lang == "ru" {
		return fmt.Sprintf("%dмин", minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
