// NavItem is one sidebar entry: a link to a screen, or (without `to`) a
// screen still to come, shown greyed as "قريبًا".
export interface NavItem {
  label: string;
  to?: string;
  pendingBadge?: boolean; // shows the number of orders waiting to be confirmed
}

export interface NavGroup {
  title?: string;
  items: NavItem[];
}

// navGroups is the sidebar of the approved mockup. Screens not built yet
// stay visible but inert, so merchants see what is coming.
export function navGroups(storeId: string): NavGroup[] {
  const at = (path: string) => `/stores/${storeId}/${path}`;
  return [
    { items: [{ label: 'الرئيسية', to: at('home') }] },
    {
      title: 'المبيعات',
      items: [
        { label: 'الطلبات', to: at('orders'), pendingBadge: true },
        { label: 'المدفوعات', to: at('payments') },
        { label: 'رسائل العملاء', to: at('messages') },
        { label: 'العملاء' },
      ],
    },
    { title: 'التسويق', items: [{ label: 'الكوبونات', to: at('coupons') }, { label: 'بكسلات الإعلانات', to: at('pixels') }, { label: 'كتالوج المنتجات', to: at('feeds') }] },
    {
      title: 'المنتجات',
      items: [
        { label: 'المنتجات', to: at('products') },
        { label: 'التصنيفات', to: at('categories') },
        { label: 'المخزون', to: at('inventory') },
        { label: 'جداول المقاسات', to: at('size-charts') },
        { label: 'الاستيراد', to: at('imports') },
      ],
    },
    {
      title: 'الشحن',
      items: [
        { label: 'الشحنات', to: at('shipments') },
        { label: 'مناطق الشحن', to: at('shipping') },
        { label: 'شركات الشحن', to: at('carriers') },
        { label: 'التحصيل', to: at('cod') },
      ],
    },
    { title: 'المتجر', items: [{ label: 'التصميم', to: at('design') }, { label: 'الصفحة الرئيسية', to: at('home-page') }, { label: 'الصفحات', to: at('pages') }, { label: 'الدومينات', to: at('domains') }] },
    { title: 'الإعدادات', items: [{ label: 'الدفع بالبطاقة', to: at('gateways') }, { label: 'فريق العمل', to: at('team') }, { label: 'الفوترة والباقة', to: at('billing') }, { label: 'إعدادات المتجر', to: at('settings') }] },
  ];
}

