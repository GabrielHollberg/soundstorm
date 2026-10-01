// The site's map, and the header, side bar, breadcrumb and next/previous
// links every page shares - built here so each page holds only its own
// writing. Plain script, no fetch: the site works opened from disk too.
//
// A page says who it is with <body data-page="server/streaming"> and where
// the site's root is with data-root="..".

const SITE = [
  { id: 'index', title: 'Overview', href: 'index.html' },
  {
    id: 'architecture', title: 'Architecture', href: 'architecture/index.html',
    pages: [
      { id: 'architecture/decisions', title: 'The decisions', href: 'architecture/decisions.html' },
      { id: 'architecture/request', title: 'A request, end to end', href: 'architecture/request.html' },
      { id: 'architecture/state', title: 'State and data', href: 'architecture/state.html' },
      { id: 'architecture/repository', title: 'The repository', href: 'architecture/repository.html' },
    ],
  },
  {
    id: 'server', title: 'The server', href: 'server/index.html',
    pages: [
      { id: 'server/http-api', title: 'HTTP API', href: 'server/http-api.html' },
      { id: 'server/accounts', title: 'Accounts and sign-in', href: 'server/accounts.html' },
      { id: 'server/search', title: 'Search and browsing', href: 'server/search.html' },
      { id: 'server/streaming', title: 'Streaming', href: 'server/streaming.html' },
      { id: 'server/provisioning', title: 'Provisioning', href: 'server/provisioning.html' },
      { id: 'server/library', title: 'The library and dropping files in', href: 'server/library.html' },
      { id: 'server/collections', title: 'Favorites, playlists, history', href: 'server/collections.html' },
      { id: 'server/music', title: 'Music: radio, moods, beats', href: 'server/music.html' },
      { id: 'server/photos', title: 'Photos: folders, backup, imports', href: 'server/photos.html' },
      { id: 'server/books', title: 'Books and read-along', href: 'server/books.html' },
      { id: 'server/tls', title: 'Certificates and names', href: 'server/tls.html' },
    ],
  },
  {
    id: 'backends', title: 'Backends', href: 'backends/index.html',
    pages: [
      { id: 'backends/navidrome', title: 'Navidrome (music)', href: 'backends/navidrome.html' },
      { id: 'backends/jellyfin', title: 'Jellyfin (films and TV)', href: 'backends/jellyfin.html' },
      { id: 'backends/audiobookshelf', title: 'Audiobookshelf', href: 'backends/audiobookshelf.html' },
      { id: 'backends/immich', title: 'Immich (photos)', href: 'backends/immich.html' },
      { id: 'backends/storyteller', title: 'Storyteller (read-along)', href: 'backends/storyteller.html' },
      { id: 'backends/audiomuse', title: 'AudioMuse-AI (moods)', href: 'backends/audiomuse.html' },
      { id: 'backends/local', title: 'Ebooks and documents', href: 'backends/local.html' },
    ],
  },
  {
    id: 'clients', title: 'Apps', href: 'clients/index.html',
    pages: [
      { id: 'clients/web', title: 'The web app', href: 'clients/web.html' },
      { id: 'clients/now-playing', title: 'Now Playing and the looks', href: 'clients/now-playing.html' },
      { id: 'clients/reader', title: 'The reader', href: 'clients/reader.html' },
      { id: 'clients/android', title: 'Android', href: 'clients/android.html' },
      { id: 'clients/iphone', title: 'iPhone', href: 'clients/iphone.html' },
      { id: 'clients/tv', title: 'TVs', href: 'clients/tv.html' },
    ],
  },
  {
    id: 'security', title: 'Security', href: 'security/index.html',
    pages: [
      { id: 'security/model', title: 'Threat model', href: 'security/model.html' },
      { id: 'security/reviews', title: 'The review passes', href: 'security/reviews.html' },
    ],
  },
  {
    id: 'operations', title: 'Running it', href: 'operations/index.html',
    pages: [
      { id: 'operations/install', title: 'Installing', href: 'operations/install.html' },
      { id: 'operations/maintain', title: 'Updating, moving, removing', href: 'operations/maintain.html' },
      { id: 'operations/remote', title: 'Away from home', href: 'operations/remote.html' },
      { id: 'operations/troubleshooting', title: 'Troubleshooting', href: 'operations/troubleshooting.html' },
    ],
  },
  {
    id: 'development', title: 'Development', href: 'development/index.html',
    pages: [
      { id: 'development/workflow', title: 'How it is developed', href: 'development/workflow.html' },
      { id: 'development/testing', title: 'Testing', href: 'development/testing.html' },
      { id: 'development/gotchas', title: 'Gotchas', href: 'development/gotchas.html' },
    ],
  },
];

(function build() {
  const body = document.body;
  const here = body.dataset.page || 'index';
  const root = (body.dataset.root || '.').replace(/\/$/, '');
  const url = (href) => `${root}/${href}`;
  const el = (tag, props = {}, ...kids) => {
    const n = document.createElement(tag);
    Object.assign(n, props);
    for (const k of kids) if (k) n.append(k);
    return n;
  };

  const section = SITE.find((s) => s.id === here || (s.pages || []).some((p) => p.id === here)) || SITE[0];
  const page = (section.pages || []).find((p) => p.id === here) || section;

  // Header: the logo and one tab per section.
  const tabs = el('nav', { className: 'tabs', ariaLabel: 'Sections' });
  for (const s of SITE) {
    const a = el('a', { href: url(s.href), textContent: s.title });
    if (s === section) a.setAttribute('aria-current', 'page');
    tabs.append(a);
  }
  const menu = el('button', { className: 'menu-button', type: 'button', textContent: 'Menu', ariaLabel: 'Show the section menu' });
  menu.addEventListener('click', () => body.classList.toggle('side-open'));
  const header = el('header', { className: 'site-header' },
    el('a', { className: 'brand', href: url('index.html') },
      el('img', { className: 'cloud', src: url('assets/favicon.svg'), alt: '' }), el('span', { textContent: 'SoundStorm' }),
      el('span', { className: 'brand-sub', textContent: 'how it works' })),
    tabs, menu);
  body.prepend(header);

  // Side bar: the section's pages.
  const main = document.querySelector('main');
  if (section.pages) {
    const list = el('ul');
    const top = el('li', {}, el('a', { href: url(section.href), textContent: section.title }));
    if (page === section) top.firstChild.setAttribute('aria-current', 'page');
    list.append(top);
    for (const p of section.pages) {
      const a = el('a', { href: url(p.href), textContent: p.title });
      if (p === page) a.setAttribute('aria-current', 'page');
      list.append(el('li', { className: 'sub' }, a));
    }
    // On a phone the section tabs are hidden, so the menu carries them too.
    const sections = el('ul', { className: 'phone-sections' });
    for (const s of SITE) {
      const a = el('a', { href: url(s.href), textContent: s.title });
      if (s === section) a.setAttribute('aria-current', 'page');
      sections.append(el('li', {}, a));
    }
    const side = el('aside', { className: 'side' }, el('nav', { ariaLabel: 'Sections' }, sections), el('nav', { ariaLabel: section.title }, list));
    main.before(side);
    body.classList.add('has-side');
  }

  // Breadcrumb.
  const crumbs = el('nav', { className: 'crumbs', ariaLabel: 'Breadcrumb' });
  crumbs.append(el('a', { href: url('index.html'), textContent: 'Overview' }));
  if (section.id !== 'index') {
    crumbs.append(' / ', page === section ? el('span', { textContent: section.title }) : el('a', { href: url(section.href), textContent: section.title }));
  }
  if (page !== section) crumbs.append(' / ', el('span', { textContent: page.title }));
  main.prepend(crumbs);

  // On this page: the h2s, when there are several.
  const heads = [...main.querySelectorAll('h2')];
  if (heads.length >= 3) {
    const toc = el('nav', { className: 'toc', ariaLabel: 'On this page' }, el('p', { textContent: 'On this page' }));
    const ul = el('ul');
    heads.forEach((h, i) => {
      if (!h.id) h.id = h.textContent.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || `part-${i}`;
      ul.append(el('li', {}, el('a', { href: `#${h.id}`, textContent: h.textContent })));
    });
    toc.append(ul);
    const h1 = main.querySelector('h1');
    (h1 && h1.nextElementSibling && h1.nextElementSibling.classList.contains('lede') ? h1.nextElementSibling : h1 || crumbs).after(toc);
  }

  // Previous and next, in reading order through the whole site.
  const order = [];
  for (const s of SITE) {
    order.push(s);
    for (const p of s.pages || []) order.push(p);
  }
  const at = order.indexOf(page);
  const pager = el('nav', { className: 'pager', ariaLabel: 'Previous and next' });
  if (at > 0) pager.append(el('a', { className: 'prev', href: url(order[at - 1].href) }, el('small', { textContent: 'Previous' }), el('span', { textContent: order[at - 1].title })));
  if (at < order.length - 1) pager.append(el('a', { className: 'next', href: url(order[at + 1].href) }, el('small', { textContent: 'Next' }), el('span', { textContent: order[at + 1].title })));
  main.append(pager);

  document.title = page === SITE[0] ? 'SoundStorm - how it works' : `${page.title} - SoundStorm`;
})();
