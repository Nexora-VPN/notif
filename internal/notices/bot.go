package notices

// What a bot answers a user who writes to it (GN-S2), per language.
var botTexts = map[string]map[string]string{
	"welcome": {
		"en": "Hello! Send me your subscription link, or the link code you were given, and I will tell you here about your account: its expiry, its traffic and any change to it.",
		"fa": "سلام! لینک اشتراک یا کد اتصالی را که به شما داده‌اند بفرستید تا خبرهای حسابتان — انقضا، ترافیک و هر تغییری — را همین‌جا به شما بدهم.",
		"ru": "Здравствуйте! Пришлите ссылку на подписку или код привязки, который вам дали, и я буду сообщать здесь о вашем аккаунте: сроке, трафике и любых изменениях.",
		"zh": "您好！请发送您的订阅链接或收到的绑定码，我会在这里通知您账户的情况：到期、流量以及任何变更。",
	},
	"linked": {
		"en": "Connected to the account {name}. Its notices will arrive here. Send /stop to disconnect.",
		"fa": "به حساب {name} وصل شد. خبرهای این حساب از این پس همین‌جا می‌رسد. برای قطع، /stop را بفرستید.",
		"ru": "Подключено к аккаунту {name}. Уведомления о нём будут приходить сюда. Чтобы отключить, отправьте /stop.",
		"zh": "已关联账户 {name}，其通知将发送到这里。发送 /stop 可取消关联。",
	},
	"notFound": {
		"en": "I could not find an account for that. Send the whole subscription link, or the link code exactly as you were given it.",
		"fa": "حسابی برای این پیدا نشد. کل لینک اشتراک یا کد اتصال را دقیقاً همان‌طور که به شما داده‌اند بفرستید.",
		"ru": "Не удалось найти аккаунт. Пришлите ссылку на подписку целиком или код привязки ровно в том виде, в каком его дали.",
		"zh": "未找到对应账户。请发送完整的订阅链接，或原样发送您收到的绑定码。",
	},
	"stopped": {
		"en": "Disconnected. No more notices will arrive here.",
		"fa": "اتصال قطع شد. دیگر خبری اینجا نمی‌رسد.",
		"ru": "Отключено. Уведомления сюда больше не придут.",
		"zh": "已取消关联，这里将不再收到通知。",
	},
	"notLinked": {
		"en": "This chat is not connected to any account.",
		"fa": "این گفتگو به هیچ حسابی وصل نیست.",
		"ru": "Этот чат не подключён ни к одному аккаунту.",
		"zh": "此会话未关联任何账户。",
	},
	"tooMany": {
		"en": "Too many tries. Please wait a few minutes.",
		"fa": "تلاش‌ها زیاد شد. چند دقیقه صبر کنید.",
		"ru": "Слишком много попыток. Подождите несколько минут.",
		"zh": "尝试次数过多，请稍等几分钟。",
	},
	"failed": {
		"en": "That did not work just now. Please try again later.",
		"fa": "الان انجام نشد. کمی بعد دوباره امتحان کنید.",
		"ru": "Сейчас не получилось. Попробуйте позже.",
		"zh": "暂时未能完成，请稍后再试。",
	},
}

// Bot is a bot's answer in a language, its variables filled.
func Bot(key, lang string, vars map[string]string) string {
	t, ok := botTexts[key][lang]
	if !ok {
		t = botTexts[key]["en"]
	}
	return fill(t, vars)
}
