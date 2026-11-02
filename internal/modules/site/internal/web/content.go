package web

// The site's copy, in Arabic (the English site comes with S3b). It speaks
// of what a merchant gains, never of how the platform is built (the
// owner's rule). The console edits the FAQ and the contact details
// later (S4); until then they live here.

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
// shows; the FAQ page shows all.
type faq struct {
	q, a string
	home bool
}

var faqs = []faq{ //nolint:gochecknoglobals // constant copy
	{"ما هي منصة افتح أونلاين؟", "افتح أونلاين (وتُكتب أيضًا افتح اونلاين، وبالإنجليزية iftahonline) منصة لإنشاء المتاجر الإلكترونية، صُممت لتجار مصر والخليج الذين يبيعون بالدفع عند الاستلام: متجر جاهز، وتأكيد الطلبات على واتساب، وحماية من الطلبات الوهمية، ولوحة للربح الصافي.", true},
	{"هل يمكنني البدء مجانًا؟", "نعم. الباقة المجانية دائمة، ولا تطلب بطاقة بنكية. حدودها في صفحة الأسعار.", true},
	{"كيف يؤكد العميل طلبه على واتساب؟", "تصله رسالة فيها تفاصيل طلبه وزرّا «تأكيد الطلب» و«إلغاء الطلب». بضغطة يتأكد الطلب في لوحتك، ومن ليس على واتساب تصله رسالة نصية برابط للتأكيد.", true},
	{"بأي عملة أدفع الاشتراك؟", "بالجنيه المصري إن كان متجرك في مصر، وبالدولار الأمريكي في باقي الدول.", true},
	{"هل أستطيع ربط دومين خاص بي؟", "نعم في الباقات المدفوعة، ويبقى لمتجرك أيضًا رابط مجاني على افتح أونلاين.", true},
	{"كيف تحمي المنصة من الطلبات الوهمية؟", "كل طلب يحصل على تقييم من نتائج توصيل رقم العميل في متاجر افتح أونلاين كلها. لا يرى أي متجر بيانات متجر آخر، فقط مستوى التقييم، وأنت تختار ما يحدث للطلبات عالية المخاطر: إيقاف للمراجعة، أو تأكيد بالهاتف، أو دفع التوصيل مقدمًا.", false},
	{"هل تعمل المنصة في الخليج؟", "نعم. المتجر يعرض الأسعار بعملته، ويعمل بالعربية والإنجليزية.", false},
	{"عندي متجر في منصة أخرى، هل يمكن نقله؟", "نعم. مع أي باقة مدفوعة ننقل منتجاتك وصورها وتصنيفاتها إلى متجرك الجديد مجانًا، ويمكنك أيضًا استيرادها بنفسك من ملف.", false},
	{"هل توجد رسوم أخرى غير الاشتراك؟", "لا رسوم مخفية. رسوم Paymob على الدفع بالبطاقة، وأسعار شركة الشحن، تدفعها لهم مباشرة بأسعارهم، ولا نأخذ منها شيئًا.", false},
	{"هل أستطيع إلغاء اشتراكي؟", "نعم، بلا عقد، في أي وقت من لوحة التاجر. وبعد نهاية المدة المدفوعة يعود متجرك إلى الباقة المجانية وتبقى بياناته كما هي.", false},
}

// worksWith are the tools the platform works with, as their makers spell
// them (the home page's strip).
var worksWith = []string{"Paymob", "ميزة", "بوسطة", "WhatsApp", "Meta", "TikTok", "Snapchat", "Google"} //nolint:gochecknoglobals // constant copy
