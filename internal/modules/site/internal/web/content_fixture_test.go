package web_test

import (
	"context"
	"sync"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
)

// fakeContent is the console's content: the FAQ the site shipped with
// (migration 00081 seeds the same), and the settings a test sets.
type fakeContent struct {
	mu  sync.Mutex
	c   domain.Content
	err error
}

func newFakeContent() *fakeContent {
	return &fakeContent{c: domain.Content{Settings: domain.Settings{WhatsApp: "+201153450716"}, FAQs: shippedFAQs()}}
}

func (f *fakeContent) Content(context.Context) (domain.Content, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.c, f.err
}

func (f *fakeContent) set(edit func(*domain.Content), err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if edit != nil {
		edit(&f.c)
	}
	f.err = err
}

func shippedFAQs() []domain.FAQ {
	return []domain.FAQ{
		{OnHome: true, QuestionAr: "ما هي منصة افتح أونلاين؟", AnswerAr: "افتح أونلاين (وتُكتب أيضًا افتح اونلاين، وبالإنجليزية iftahonline) منصة لإنشاء المتاجر الإلكترونية، صُممت لتجار مصر والخليج الذين يبيعون بالدفع عند الاستلام: متجر جاهز، وتأكيد الطلبات على واتساب، وحماية من الطلبات الوهمية، ولوحة للربح الصافي.",
			QuestionEn: "What is iftahonline?", AnswerEn: "iftahonline (also written eftahonline, and in Arabic افتح أونلاين) is a platform for creating online stores, built for merchants in Egypt and the Gulf who sell with cash on delivery: a ready store, order confirmation on WhatsApp, protection from fake orders, and a net profit dashboard."},
		{OnHome: true, QuestionAr: "هل يمكنني البدء مجانًا؟", AnswerAr: "نعم. الباقة المجانية دائمة، ولا تطلب بطاقة بنكية. حدودها في صفحة الأسعار.",
			QuestionEn: "Can I start free?", AnswerEn: "Yes. The free plan is forever and needs no card. Its limits are on the pricing page."},
		{OnHome: true, QuestionAr: "كيف يؤكد العميل طلبه على واتساب؟", AnswerAr: "تصله رسالة فيها تفاصيل طلبه وزرّا «تأكيد الطلب» و«إلغاء الطلب». بضغطة يتأكد الطلب في لوحتك، ومن ليس على واتساب تصله رسالة نصية برابط للتأكيد.",
			QuestionEn: "How does a customer confirm an order on WhatsApp?", AnswerEn: "They get a message with their order details and «Confirm order» and «Cancel order» buttons. One tap confirms the order in your dashboard, and customers who aren't on WhatsApp get a text message with a confirmation link."},
		{OnHome: true, QuestionAr: "بأي عملة أدفع الاشتراك؟", AnswerAr: "بالجنيه المصري إن كان متجرك في مصر، وبالدولار الأمريكي في باقي الدول.",
			QuestionEn: "Which currency do I pay in?", AnswerEn: "In Egyptian pounds if your store is in Egypt, and in US dollars in every other country."},
		{OnHome: true, QuestionAr: "هل أستطيع ربط دومين خاص بي؟", AnswerAr: "نعم في الباقات المدفوعة، ويبقى لمتجرك أيضًا رابط مجاني على افتح أونلاين.",
			QuestionEn: "Can I connect my own domain?", AnswerEn: "Yes, on paid plans, and your store keeps a free iftahonline link too."},
		{OnHome: false, QuestionAr: "كيف تحمي المنصة من الطلبات الوهمية؟", AnswerAr: "كل طلب يحصل على تقييم من نتائج توصيل رقم العميل في متاجر افتح أونلاين كلها. لا يرى أي متجر بيانات متجر آخر، فقط مستوى التقييم، وأنت تختار ما يحدث للطلبات عالية المخاطر: إيقاف للمراجعة، أو تأكيد بالهاتف، أو دفع التوصيل مقدمًا.",
			QuestionEn: "How does the platform protect from fake orders?", AnswerEn: "Every order gets a rating from the delivery results of the customer's number across all iftahonline stores. No store sees another store's data, only the rating level, and you choose what happens to high-risk orders: hold for review, confirm by phone, or ask for the delivery fee upfront."},
		{OnHome: false, QuestionAr: "هل تعمل المنصة في الخليج؟", AnswerAr: "نعم. المتجر يعرض الأسعار بعملته، ويعمل بالعربية والإنجليزية.",
			QuestionEn: "Does the platform work in the Gulf?", AnswerEn: "Yes. The store shows prices in its own currency, in Arabic and English."},
		{OnHome: false, QuestionAr: "عندي متجر في منصة أخرى، هل يمكن نقله؟", AnswerAr: "نعم. مع أي باقة مدفوعة ننقل منتجاتك وصورها وتصنيفاتها إلى متجرك الجديد مجانًا، ويمكنك أيضًا استيرادها بنفسك من ملف.",
			QuestionEn: "I have a store on another platform. Can I move it?", AnswerEn: "Yes. With any paid plan we move your products, their photos and categories to your new store for free, and you can also import them yourself from a file."},
		{OnHome: false, QuestionAr: "هل توجد رسوم أخرى غير الاشتراك؟", AnswerAr: "لا رسوم مخفية. رسوم Paymob على الدفع بالبطاقة، وأسعار شركة الشحن، تدفعها لهم مباشرة بأسعارهم، ولا نأخذ منها شيئًا.",
			QuestionEn: "Are there fees besides the subscription?", AnswerEn: "No hidden fees. Paymob's card fees and your carrier's rates are paid to them directly at their prices, and we take nothing from them."},
		{OnHome: false, QuestionAr: "هل أستطيع إلغاء اشتراكي؟", AnswerAr: "نعم، بلا عقد، في أي وقت من لوحة التاجر. وبعد نهاية المدة المدفوعة يعود متجرك إلى الباقة المجانية وتبقى بياناته كما هي.",
			QuestionEn: "Can I cancel my subscription?", AnswerEn: "Yes, with no contract, any time from your dashboard. When the paid period ends your store returns to the free plan and its data stays as it is."},
	}
}
