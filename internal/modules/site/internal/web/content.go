package web

// The site's copy, in Arabic (the English is in content_en.go). It speaks
// of what a merchant gains, never of how the platform is built (the
// owner's rule). The FAQ and the contact details are the console's
// (S4a, ADR-0073).

// brand is how the platform is named. Its spelling variants are said
// once, naturally (the FAQ and llms.txt), never stuffed.
const (
	brandAR       = "افتح أونلاين"
	brandLatin    = "iftahonline"
	brandVariants = "افتح اونلاين، افتح اون لاين، iftahonline، eftahonline"
	tagline       = "منصة متاجر إلكترونية للدفع عند الاستلام، لتجار مصر والخليج."
	// updated is when the copy last changed: sitemap.xml's lastmod.
	updated = "2026-11-02"
)

// company is who runs the platform (the about page shows it; here it
// feeds the schema.org data only).
var company = struct{ name, street, city, postcode, country string }{ //nolint:gochecknoglobals // constant copy
	"Codezilo Ltd", "5c New Quebec Street", "London", "W1H 7RG", "GB",
}

// feature is one of the home page's alternating rows.
type feature struct {
	id, tag, tagClass, title, body string
	points                         []string
	more                           string // the feature's own page
}

var features = []feature{ //nolint:gochecknoglobals // constant copy
	{
		id: "confirm", tag: "تأكيد الطلبات", tagClass: "green", more: "/features/whatsapp-confirmation",
		title: "كل طلب يؤكده صاحبه على واتساب، دون أن تتصل",
		body:  "يصل العميلَ طلبُه برسالة واتساب باسم متجرك وزرّي «تأكيد» و«إلغاء». ومن ليس على واتساب تصله رسالة نصية برابط. وما لم يُرد عليه يُلغى في المهلة التي تحددها.",
		points: []string{
			"العميل يؤكد بنفسه من صفحة الشكر مجانًا",
			"سجل لكل رسالة: أُرسلت، وصلت، قُرئت",
			"إلغاء تلقائي بعد مهلة من ساعة إلى 72 ساعة",
		},
	},
	{
		id: "protect", tag: "الحماية من الطلبات الوهمية", tagClass: "amber", more: "/features/fake-orders-protection",
		title: "اعرف من يرفض الاستلام قبل أن تشحن له",
		body:  "كل طلب يحصل على تقييم من نتائج التوصيل في متاجر افتح أونلاين كلها: سُلّم، رُفض، لم يرد. وأنت تختار ما يحدث لكل مستوى: إيقاف للمراجعة، أو تأكيد بالهاتف، أو دفع التوصيل مقدمًا. لا يرى أي متجر بيانات متجر آخر، فقط مستوى التقييم.",
	},
	{
		id: "profit", tag: "الربح الصافي", tagClass: "blue", more: "/features/net-profit",
		title: "المبيعات ليست ربحًا. نريك ما بقي لك فعلًا",
		body:  "نطرح من مبيعاتك تكلفة المنتج، والشحن، ورسوم التحصيل، والمرتجعات، والإعلانات، ونريك الربح لكل منتج وحملة إعلانية ومحافظة.",
	},
}

// tool is a tile of the "every tool" grid; wide spans two columns.
type tool struct {
	title, body string
	wide        bool
}

var tools = []tool{ //nolint:gochecknoglobals // constant copy
	{"متجر احترافي بالعربية والإنجليزية", "ثلاثة أشكال جاهزة تختار منها، ومحرر للصفحة الرئيسية بالأقسام، وصفحة هبوط لكل منتج بطلب من خطوة واحدة، ودومين خاص بك.", true},
	{"الشحن والتحصيل", "أسعار لكل محافظة ومدينة، وشحنات تُنشأ وتُتابع مع شركة الشحن.", false},
	{"الدفع بالبطاقة", "اربط حساب Paymob الخاص بك، فيصلك المال مباشرة.", false},
	{"بكسلات الإعلانات", "Meta وTikTok وSnapchat وGoogle، مع قياس أدق لنتائج إعلاناتك.", false},
	{"الكوبونات والعروض", "خصم بنسبة أو مبلغ، بحد أدنى وتاريخ انتهاء.", false},
	{"كتالوج المنتجات", "ملف منتجات يتحدث وحده لإعلانات Meta وGoogle وTikTok.", false},
	{"استيراد المنتجات", "انقل منتجاتك من ملف أو من متجرك القديم.", false},
	{"فريق العمل", "أضف موظفيك بصلاحيات: طلبات، شحن، مالية.", false},
}

// step is one of the three steps to a first order.
type step struct{ title, body string }

var steps = []step{ //nolint:gochecknoglobals // constant copy
	{"أنشئ متجرك", "سجّل ببريدك، واختر اسم متجرك وشكله وألوانه، ويصبح له رابط فورًا."},
	{"أضف منتجاتك", "بالصور والمقاسات والألوان، أو استوردها كلها مرة واحدة."},
	{"ابدأ البيع", "شارك رابط متجرك في إعلاناتك، وتابع الطلبات المؤكدة وربحك من لوحة واحدة."},
}

// trust is one of the "your store is safe" cards.
type trust struct{ icon, title, body string }

var trustItems = []trust{ //nolint:gochecknoglobals // constant copy
	{"lock", "بيانات البطاقات لا تمر بنا", "يدفع عميلك على صفحة Paymob المرخصة، ويصلك المال مباشرة."},
	{"people", "عملاؤك لك وحدك", "لا يرى أي متجر آخر عملاءك أو طلباتك، ولا نبيع بياناتهم لأحد، ولا نراسلهم إلا بشأن طلباتهم من متجرك."},
	{"download", "بياناتك ملكك", "بلا عقد، وتلغي متى شئت، وتُصدّر منتجاتك وطلباتك في أي وقت."},
	{"chat", "دعم بالعربية على واتساب", "فريق يرد عليك بالعربية، من أول يوم."},
}

// faq is one question and its answer. home marks the ones the home page
// shows; the FAQ page shows all. The site's FAQ is the console's (S4a);
// the feature pages keep their own questions here.
type faq struct {
	q, a string
	home bool
}

// worksWith are the tools the platform works with, as their makers spell
// them (the home page's strip).
var worksWith = []string{"Paymob", "ميزة", "بوسطة", "WhatsApp", "Meta", "TikTok", "Snapchat", "Google"} //nolint:gochecknoglobals // constant copy
