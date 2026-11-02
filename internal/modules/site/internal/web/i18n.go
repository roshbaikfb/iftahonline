package web

import "strings"

// The site speaks Arabic at / and English under /en/ (S3b). One set of
// templates serves both: every word they show comes from a language's
// texts and copy.

// language is one of the site's two: its words, its copy, and where its
// pages live.
type language struct {
	code, dir, locale string // "ar", "rtl", "ar_EG"
	base              string // the path prefix: "" or "/en"
	t                 *texts
	c                 *siteCopy
}

var (
	arabic  = &language{code: "ar", dir: "rtl", locale: "ar_EG", base: "", t: &arTexts, c: &arCopy}    //nolint:gochecknoglobals // constant copy
	english = &language{code: "en", dir: "ltr", locale: "en_US", base: "/en", t: &enTexts, c: &enCopy} //nolint:gochecknoglobals // constant copy
)

// at is a site path in the language: "/pricing" → "/en/pricing", "/" →
// "/en".
func (l *language) at(path string) string {
	if l.base == "" {
		return path
	}
	if path == "/" {
		return l.base
	}
	return l.base + path
}

// siteCopy is a language's longer copy: the lists the pages show.
type siteCopy struct {
	brand        string
	tagline      string
	features     []feature
	tools        []tool
	steps        []step
	trust        []trust
	worksWith    []string
	featurePages []featurePage
	values       []step
	privacy      legalPage
	terms        legalPage
	legalUpdated string
}

var arCopy = siteCopy{ //nolint:gochecknoglobals // constant copy
	brand: brandAR, tagline: tagline, features: features, tools: tools, steps: steps, trust: trustItems,
	worksWith: worksWith, featurePages: featurePages, values: values, privacy: privacy, terms: terms, legalUpdated: legalUpdated,
}

// texts are a language's short words: labels, headings, buttons, titles.
type texts struct {
	Skip, ChatUs, StartFree, SignIn, Menu, MainMenu, Features, Pricing, FAQ, About, Home, Crumbs string
	FooterTagline, Platform, Contact, WhatsApp, Rights, Legal, OtherLang, OtherLangName          string
	Arrow, WhatsAppHello                                                                         string
	WhatsAppIntl, Email, FollowUs, CompanyNo                                                     string

	HomeTitle, HomeDesc, PricingTitle, PricingDesc, FAQTitle, FAQDesc string
	FeaturesTitle, FeaturesDesc, AboutTitle, AboutDesc, NotFoundTitle string

	HeroH1, HeroAccent, HeroLead, CreateFree, SeePrices, HeroFine, WorksWith            string
	WhyEyebrow, WhyH2, WhyLead, LearnMore, ToolsEyebrow, ToolsH2, StepsH2, Step         string
	TrustEyebrow, TrustH2, MoveTag, MoveH2, MoveLead, MoveCTA                           string
	MoveSteps                                                                           [3]string
	PricingEyebrow, PricingH2, PricingLead, Recommended, NoHidden, CompareAll           string
	FAQEyebrow, FAQH2, NoAnswer, TalkWhatsApp, CTAH2, CTALead, TalkToUs                 string
	NotFoundH1, NotFoundLead, HomePage                                                  string
	PricingH1, PricingLead2, PricesIn, CurrencyEGP, CurrencyUSD, ShowUSD, ShowEGP       string
	Monthly, Annual, Cycle, Plans, USDSoon, ContactWhatsApp                             string
	ExtraNoHidden, ExtraNoHiddenBody, ExtraMove, ExtraMoveBody, ExtraNoContract         string
	ExtraNoContractBody, CompareH2, Feature, Always, PerDelivered, PerMonth, PerYear    string
	Soon, Trial, FAQH1, FAQLead                                                         string
	FeaturesH1, FeaturesLead, PaidOnly, ReadMore, QuestionsAbout, OtherFeatures         string
	AboutH1, AboutLead, WhyH2About, WhyP1, WhyP2, ValuesH2, CompanyH2, RegisteredUK     string
	BrandOf, ContactLead, UKFlag, LastUpdated                                           string
	PlanFree, PlanPays, PlanTrial, PlanSubscribe                                        string
	Unlimited, Yes, Products, Orders, Messages, Staff, ProductsAll, OrdersAll           string
	MessagesAll, StaffAll, Protection, Profit, Cards, Domain                            string
	Singular                                                                            map[string]string // a count's word for one, where it differs
	Example                                                                             string
	ShotLabel, ShotStore, ShotNav, ShotOrders, ShotNew, ShotConfirmed, ShotHeld         string
	ShotRows                                                                            [4][3]string
	ToastTitle, ToastOrder, ToastBody                                                   string
	ProfitFigures                                                                       [5]string
	ChatIn1, ChatConfirm, ChatCancel, ChatIn2                                           string
	RiskTitle, RiskHigh, RiskWhy, RiskRule, RiskPrepay, RiskHold, RiskPhone             string
	ProfitHead, ProfitRevenue, ProfitCost, ProfitShipping, ProfitAds                    string
	ShipTitle, ShipStatus, ShipConfirmed, ShipPicked, ShipOnWay, ShipDelivered, ShipFee string
	ShipZones                                                                           [4][2]string
	StoreProduct, StorePrice, StoreWas, StoreName, StorePhone, StoreArea, StoreBuy      string
	MktPixels, MktOn, MktOff, MktCoupon                                                 string
}

// ShotNav is the sample dashboard's menu, ShotRows its orders; the
// amounts are written as each language writes them.

var arTexts = texts{ //nolint:gochecknoglobals // constant copy
	Skip: "انتقل إلى المحتوى", ChatUs: "تواصل معنا على واتساب", StartFree: "ابدأ مجانًا", SignIn: "تسجيل الدخول", Menu: "القائمة",
	MainMenu: "القائمة الرئيسية", Features: "المميزات", Pricing: "الأسعار", FAQ: "الأسئلة الشائعة", About: "عن افتح أونلاين",
	Home: "الرئيسية", Crumbs: "مسار الصفحة",
	FooterTagline: "منصة متاجر إلكترونية للدفع عند الاستلام، لتجار مصر والخليج.", Platform: "المنصة", Contact: "تواصل معنا",
	WhatsApp: "واتساب", Rights: "© 2026 افتح أونلاين. جميع الحقوق محفوظة.", Legal: "الصفحات القانونية",
	OtherLang: "en", OtherLangName: "English", Arrow: "←", WhatsAppHello: "مرحبًا، أريد الاستفسار عن افتح أونلاين",
	WhatsAppIntl: "واتساب (دولي)", Email: "البريد", FollowUs: "تابعنا", CompanyNo: "رقم التسجيل في Companies House:",

	HomeTitle:     "افتح أونلاين | منصة متاجر إلكترونية للدفع عند الاستلام",
	HomeDesc:      "أنشئ متجرك الإلكتروني مجانًا على افتح أونلاين: تأكيد الطلبات على واتساب، وحماية من الطلبات الوهمية، ولوحة للربح الصافي. لتجار مصر والخليج.",
	PricingTitle:  "أسعار افتح أونلاين | ابدأ مجانًا",
	PricingDesc:   "أسعار افتح أونلاين: باقة مجانية دائمًا، أو بلا اشتراك بنسبة من الطلبات المسلّمة، أو باقات شهرية وسنوية بتجربة 14 يومًا. بالجنيه في مصر وبالدولار في باقي الدول.",
	FAQTitle:      "الأسئلة الشائعة عن افتح أونلاين",
	FAQDesc:       "إجابات واضحة عن افتح أونلاين: البدء مجانًا، وتأكيد الطلبات على واتساب، والحماية من الطلبات الوهمية، والأسعار بالجنيه والدولار، ونقل متجرك.",
	FeaturesTitle: "مميزات افتح أونلاين | كل ما يحتاجه متجر الدفع عند الاستلام",
	FeaturesDesc:  "مميزات افتح أونلاين: تأكيد الطلبات على واتساب، والحماية من الطلبات الوهمية، ولوحة الربح الصافي، والشحن والتحصيل، ومتجر احترافي، وأدوات التسويق.",
	AboutTitle:    "عن افتح أونلاين | من نحن",
	AboutDesc:     "افتح أونلاين منصة متاجر إلكترونية لتجار الدفع عند الاستلام في مصر والخليج، علامة تجارية لشركة Codezilo Ltd المسجلة في المملكة المتحدة.",
	NotFoundTitle: "الصفحة غير موجودة | افتح أونلاين",

	HeroH1: "المنصة التي تبني متجرك الإلكتروني", HeroAccent: "للدفع عند الاستلام",
	HeroLead:   "أنشئ متجرك في دقائق، وأكّد كل طلب على واتساب، واعرف العميل الذي يرفض الاستلام قبل أن تشحن له، وتابع ربحك الصافي الحقيقي. افتح أونلاين لتجار مصر والخليج.",
	CreateFree: "أنشئ متجرك مجانًا", SeePrices: "شاهد الأسعار", HeroFine: "التسجيل في دقيقة · باقة مجانية دائمًا · بدون بطاقة بنكية · بلا عقد",
	WorksWith:  "يعمل مع أدوات تعرفها",
	WhyEyebrow: "لماذا افتح أونلاين", WhyH2: "صُممت حول ما يخسّر تاجر الدفع عند الاستلام",
	WhyLead:   "الطلبات التي لا تُؤكَّد، والطرود التي ترجع، والربح الذي لا تراه. كل أداة هنا بُنيت لواحدة منها.",
	LearnMore: "اعرف المزيد عن", ToolsEyebrow: "كل الأدوات", ToolsH2: "كل ما يحتاجه متجرك، في مكان واحد",
	StepsH2: "من التسجيل إلى أول طلب، في نفس اليوم", Step: "الخطوة",
	TrustEyebrow: "الأمان والثقة", TrustH2: "متجرك وبياناتك في أمان",
	MoveTag: "مجانًا مع الاشتراك", MoveH2: "عندك متجر في منصة أخرى؟ ننقله لك مجانًا",
	MoveLead:       "اشترك في أي باقة مدفوعة، وفريقنا ينقل منتجاتك وصورها وتصنيفاتها إلى متجرك الجديد، دون أن تتوقف مبيعاتك.",
	MoveCTA:        "اطلب نقل متجرك",
	MoveSteps:      [3]string{"أرسل لنا رابط متجرك الحالي", "ننقل المنتجات والصور والتصنيفات", "تراجع متجرك الجديد وتبدأ البيع"},
	PricingEyebrow: "الأسعار", PricingH2: "ابدأ مجانًا، وادفع حين تكبر",
	PricingLead: "الأسعار بعملة بلدك: بالجنيه في مصر، وبالدولار في باقي الدول. كل الباقات المدفوعة تبدأ بتجربة مجانية.",
	Recommended: "موصى بها", NoHidden: "لا رسوم مخفية: رسوم Paymob وشركة الشحن تدفعها لهم مباشرة بأسعارهم، ولا نأخذ منها شيئًا.",
	CompareAll: "قارن كل الباقات", FAQEyebrow: "الأسئلة الشائعة", FAQH2: "كل ما تريد معرفته عن افتح أونلاين",
	NoAnswer: "لم تجد إجابتك؟", TalkWhatsApp: "كلّمنا على واتساب", CTAH2: "افتح متجرك أونلاين اليوم",
	CTALead: "ابدأ بالباقة المجانية، وطوّرها حين يكبر متجرك.", TalkToUs: "تحدث معنا",
	NotFoundH1: "الصفحة غير موجودة", NotFoundLead: "ربما تغيّر الرابط. ابدأ من الصفحة الرئيسية.", HomePage: "الصفحة الرئيسية",
	PricingH1:    "أسعار واضحة، تبدأ من الصفر",
	PricingLead2: "ابدأ بالباقة المجانية، أو بلا اشتراك وادفع نسبة مما يُسلَّم فقط، أو اختر باقة شهرية أو سنوية. كل الباقات المدفوعة تبدأ بتجربة مجانية.",
	PricesIn:     "الأسعار بـ", CurrencyEGP: "الجنيه المصري", CurrencyUSD: "الدولار الأمريكي", ShowUSD: "اعرضها بالدولار", ShowEGP: "اعرضها بالجنيه",
	Monthly: "شهري", Annual: "سنوي", Cycle: "مدة الاشتراك", Plans: "الباقات",
	USDSoon: "الأسعار بالدولار قريبًا. لمعرفة أسعار بلدك الآن،", ContactWhatsApp: "تواصل معنا على واتساب",
	ExtraNoHidden: "لا رسوم مخفية", ExtraNoHiddenBody: "رسوم Paymob على الدفع بالبطاقة، وأسعار شركة الشحن، تدفعها لهم مباشرة بأسعارهم. لا نأخذ منها شيئًا.",
	ExtraMove: "نقل متجرك مجانًا", ExtraMoveBody: "مع أي باقة مدفوعة، ننقل منتجاتك من منصتك الحالية.",
	ExtraNoContract: "بلا عقد", ExtraNoContractBody: "ألغِ في أي وقت من لوحة التاجر، وتبقى بياناتك ملكك.",
	CompareH2: "قارن الباقات بالتفصيل", Feature: "الميزة", Always: "دائمًا", PerDelivered: "من كل طلب يصل لعميلك",
	PerMonth: "في الشهر", PerYear: "في السنة", Soon: "قريبًا", Trial: "تجربة مجانية",
	FAQH1: "الأسئلة الشائعة عن افتح أونلاين", FAQLead: "كل ما يسأل عنه التجار قبل أن يبدؤوا: التكاليف، والتأكيد، والحماية، ونقل المتاجر.",
	FeaturesH1:   "كل ما يحتاجه متجر الدفع عند الاستلام",
	FeaturesLead: "من أول طلب حتى الربح الصافي: أدوات صُممت لما يواجهه تاجر الدفع عند الاستلام في مصر والخليج كل يوم.",
	PaidOnly:     "في الباقات المدفوعة", ReadMore: "اقرأ المزيد", QuestionsAbout: "أسئلة عن", OtherFeatures: "مميزات أخرى في افتح أونلاين",
	AboutH1: "عن افتح أونلاين", AboutLead: "نبني الأدوات التي يحتاجها تاجر الدفع عند الاستلام في مصر والخليج، ليبيع أكثر ويخسر أقل.",
	WhyH2About: "لماذا افتح أونلاين؟",
	WhyP1:      "في مصر والخليج يدفع كثير من العملاء عند الاستلام. لكن أغلب منصات المتاجر صُممت لمن يدفع بالبطاقة: لا تؤكد الطلب مع العميل، ولا تنبهك إلى من يرفض الاستلام، ولا تريك ما يضيع من ربحك بين الشحن والمرتجعات والإعلانات.",
	WhyP2:      "بنينا افتح أونلاين لتسد هذه الفجوة: متجر بالعربية جاهز في دقائق، وكل طلب يؤكده صاحبه على واتساب، وتقييم لكل طلب قبل أن تشحنه، ولوحة تريك ربحك الصافي الحقيقي.",
	ValuesH2:   "ما نؤمن به", CompanyH2: "الشركة", RegisteredUK: "شركة مسجلة في المملكة المتحدة",
	BrandOf: "افتح أونلاين علامة تجارية تملكها وتديرها شركة Codezilo Ltd.", ContactLead: "فريقنا يرد عليك بالعربية، من أول يوم.",
	UKFlag: "علم المملكة المتحدة", LastUpdated: "آخر تحديث:",
	PlanFree: "ابدأ مجانًا", PlanPays: "ابدأ بلا اشتراك", PlanTrial: "جرّبها مجانًا", PlanSubscribe: "اشترك الآن",
	Unlimited: "بلا حد", Yes: "نعم", Products: "منتج", Orders: "طلب في الشهر", Messages: "رسالة واتساب في الشهر", Staff: "من فريق العمل",
	ProductsAll: "منتجات بلا حد", OrdersAll: "طلبات بلا حد", MessagesAll: "رسائل واتساب بلا حد", StaffAll: "فريق عمل بلا حد",
	Protection: "الحماية من الطلبات الوهمية", Profit: "لوحة الربح الصافي", Cards: "الدفع بالبطاقة", Domain: "دومين خاص بك",
	Example:   "مثال توضيحي",
	ShotLabel: "مثال من لوحة التاجر", ShotStore: "متجر سوق", ShotNav: "الرئيسية|الطلبات|المنتجات|رسائل العملاء|الربح الصافي",
	ShotOrders: "الطلبات", ShotNew: "طلبات جديدة", ShotConfirmed: "أكّدها العملاء على واتساب", ShotHeld: "موقوفة للمراجعة",
	ShotRows: [4][3]string{
		{"منى · القاهرة", "مؤكد", "515 ج.م"}, {"أحمد · الجيزة", "تم الشحن", "320 ج.م"},
		{"سارة · طنطا", "موقوف للمراجعة", "450 ج.م"}, {"خالد · دبي", "بانتظار التأكيد", "149 د.إ"},
	},
	ToastTitle: "أكّدت منى طلبها على واتساب", ToastOrder: "الطلب", ToastBody: "جاهز للشحن",
	ProfitFigures: [5]string{"18,450 ج.م", "62,300 ج.م", "−27,900 ج.م", "−9,750 ج.م", "−6,200 ج.م"},
	ChatIn1:       "أهلاً منى، شكراً لطلبك من متجر سوق. رقم الطلب 7P0R1S2T والإجمالي 515 ج.م. هل تؤكد الطلب؟",
	ChatConfirm:   "تأكيد الطلب", ChatCancel: "إلغاء الطلب", ChatIn2: "شكراً منى، تم تأكيد طلبك. سنبلغك عند شحنه.",
	RiskTitle: "تقييم العميل", RiskHigh: "مخاطر مرتفعة", RiskWhy: "رفض الاستلام مرتين في متجرين آخرين خلال الشهور الأخيرة.",
	RiskRule: "قاعدتك لهذا المستوى", RiskPrepay: "اطلب دفع التوصيل مقدمًا", RiskHold: "أوقفه للمراجعة", RiskPhone: "تأكيد بالهاتف",
	ProfitHead: "صافي الربح · 30 يومًا", ProfitRevenue: "الإيراد", ProfitCost: "تكلفة المنتجات", ProfitShipping: "الشحن والمرتجعات", ProfitAds: "الإعلانات",
	ShipTitle: "شحنة الطلب", ShipStatus: "في الطريق", ShipConfirmed: "تأكد الطلب على واتساب", ShipPicked: "استلمته شركة الشحن",
	ShipOnWay: "في الطريق إلى الجيزة", ShipDelivered: "التسليم وتحصيل 320 ج.م", ShipFee: "أسعار الشحن",
	ShipZones:    [4][2]string{{"القاهرة والجيزة", "60 ج.م"}, {"الإسكندرية والدلتا", "70 ج.م"}, {"الصعيد", "85 ج.م"}, {"فوق 1,000 ج.م", "شحن مجاني"}},
	StoreProduct: "فستان صيفي قطن", StorePrice: "450 ج.م", StoreWas: "600 ج.م", StoreName: "الاسم", StorePhone: "رقم الهاتف",
	StoreArea: "المحافظة والمدينة", StoreBuy: "اطلب الآن، والدفع عند الاستلام",
	MktPixels: "بكسلات الإعلانات", MktOn: "متصل", MktOff: "غير متصل", MktCoupon: "خصم 20٪ على الطلبات فوق 500 ج.م، حتى نهاية الشهر",
}

// The sample dashboard's order references and their status chips, the
// same in both languages.
var (
	shotRefs  = [4]string{"#7P0R1S2T", "#K8M2Q4ZX", "#3HV9TB6N", "#W2N7C5LD"} //nolint:gochecknoglobals // constant copy
	shotChips = [4]string{"ok", "info", "warn", ""}                           //nolint:gochecknoglobals // constant copy
)

// shotNav is the sample dashboard's menu.
func shotNav(t *texts) []string { return strings.Split(t.ShotNav, "|") }
