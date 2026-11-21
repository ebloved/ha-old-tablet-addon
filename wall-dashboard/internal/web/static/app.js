(function () {
  'use strict';

  var TOKEN_KEY = 'otd_token';

  var ACTION_DOMAINS = { script: true, scene: true, button: true, input_button: true };

  var CONDITIONS = {
    'clear-night': '🌙 Ясно',
    sunny: '☀️ Солнечно',
    partlycloudy: '⛅ Переменная облачность',
    cloudy: '☁️ Облачно',
    rainy: '🌧️ Дождь',
    pouring: '🌧️ Ливень',
    snowy: '❄️ Снег',
    'snowy-rainy': '🌨️ Снег с дождём',
    fog: '🌫️ Туман',
    hail: '🌨️ Град',
    lightning: '⚡ Гроза',
    'lightning-rainy': '⛈️ Гроза с дождём',
    windy: '💨 Ветрено',
    'windy-variant': '💨 Ветрено',
    exceptional: '⚠️ Аномалия'
  };

  var token = '';
  try {
    token = localStorage.getItem(TOKEN_KEY) || '';
  } catch (e) {
    token = '';
  }

  var m = /[?&]t=([^&]+)/.exec(location.search);
  if (m) {
    token = decodeURIComponent(m[1]);
    try {
      localStorage.setItem(TOKEN_KEY, token);
    } catch (e) {
      /* ignore */
    }
  }

  var grid = document.getElementById('grid');
  var dot = document.getElementById('status');
  var toastEl = document.getElementById('toast');

  var cfg = null;
  var states = {};
  var tiles = [];
  var es = null;
  var lastEvent = Date.now();
  var reloading = false;
  var noTokenShown = false;
  var camTimers = [];

  function api(path, opts) {
    opts = opts || {};
    opts.headers = opts.headers || {};
    opts.headers['X-Dash-Token'] = token;
    return fetch(path, opts);
  }

  function pad(n) {
    return n < 10 ? '0' + n : '' + n;
  }

  function parseHM(s) {
    var m = /^([01][0-9]|2[0-3]):([0-5][0-9])$/.exec(String(s || ''));
    if (!m) {
      return null;
    }
    return parseInt(m[1], 10) * 60 + parseInt(m[2], 10);
  }

  function timeIsDark(c) {
    var now = new Date();
    var mins = now.getHours() * 60 + now.getMinutes();
    var d = parseHM(c.theme_dark_from);
    var l = parseHM(c.theme_light_from);
    if (d === null) {
      d = 21 * 60;
    }
    if (l === null) {
      l = 7 * 60;
    }
    if (d === l) {
      return false;
    }
    if (d < l) {
      return mins >= d && mins < l;
    }
    return mins >= d || mins < l;
  }

  function effectiveTheme(c) {
    if (!c) {
      return 'dark';
    }
    if (c.theme_mode === 'time') {
      return timeIsDark(c) ? 'dark' : 'light';
    }
    return c.theme === 'light' ? 'light' : 'dark';
  }

  function relTime(iso) {
    if (!iso) {
      return '';
    }
    var t = Date.parse(iso);
    if (isNaN(t)) {
      return '';
    }
    var s = Math.round((Date.now() - t) / 1000);
    if (s < 60) {
      return 'только что';
    }
    if (s < 3600) {
      return Math.round(s / 60) + ' мин назад';
    }
    if (s < 86400) {
      return Math.round(s / 3600) + ' ч назад';
    }
    return Math.round(s / 86400) + ' дн назад';
  }

  function defaultService(entity, st) {
    var domain = (entity || '').split('.')[0];
    if (domain === 'script' || domain === 'scene') {
      return 'turn_on';
    }
    if (domain === 'button' || domain === 'input_button') {
      return 'press';
    }
    if (domain === 'cover') {
      return st && (st.state === 'open' || st.state === 'opening') ? 'close_cover' : 'open_cover';
    }
    return 'toggle';
  }

  function toast(msg) {
    toastEl.textContent = msg;
    toastEl.classList.add('show');
    setTimeout(function () {
      toastEl.classList.remove('show');
    }, 2500);
  }

  function entityState(id) {
    return states[id] || null;
  }

  function friendly(w) {
    if (w.name) {
      return w.name;
    }
    var st = entityState(w.entity);
    if (st && st.attributes && st.attributes.friendly_name) {
      return String(st.attributes.friendly_name);
    }
    return w.entity || w.type;
  }

  function isOn(state) {
    return (
      state === 'on' ||
      state === 'open' ||
      state === 'opening' ||
      state === 'playing' ||
      state === 'heat' ||
      state === 'cool' ||
      state === 'home'
    );
  }

  function buildTile(w) {
    var node = document.createElement('div');
    node.className = 'tile t-' + w.type;
    var tracks = (cfg.columns || 6) * 2;
    var span = Math.max(1, Math.min(tracks, Math.round((w.w || 1) * 2)));
    var x = w.x || 0;
    if (x + span > tracks) {
      x = Math.max(0, tracks - span);
    }
    node.style.gridColumn = x + 1 + ' / span ' + span;
    node.style.gridRow = (w.y || 0) + 1 + ' / span ' + Math.min(6, w.h || 1);

    if (w.type === 'spacer') {
      return node;
    }

    if (w.type === 'clock') {
      var cn = document.createElement('div');
      cn.className = 'name';
      cn.textContent = w.name || '';
      var cv = document.createElement('div');
      cv.className = 'value time';
      var cd = document.createElement('div');
      cd.className = 'date';
      node.appendChild(cn);
      node.appendChild(cv);
      node.appendChild(cd);
      startClock(node);
      return node;
    }

    if (w.type === 'weather') {
      var wn = document.createElement('div');
      wn.className = 'name';
      wn.textContent = friendly(w);
      var wv = document.createElement('div');
      wv.className = 'value';
      wv.textContent = '—';
      var ws = document.createElement('div');
      ws.className = 'sub';
      node.appendChild(wn);
      node.appendChild(wv);
      node.appendChild(ws);
      return node;
    }

    if (w.type === 'alerts') {
      var an = document.createElement('div');
      an.className = 'name';
      an.textContent = w.name || 'Алерты';
      var abox = document.createElement('div');
      abox.className = 'alerts';
      node.appendChild(an);
      node.appendChild(abox);
      return node;
    }

    if (w.type === 'camera') {
      var img = document.createElement('img');
      img.className = 'cam';
      img.alt = '';
      var camName = document.createElement('div');
      camName.className = 'cam-name';
      camName.textContent = friendly(w);
      node.appendChild(img);
      node.appendChild(camName);
      startCamera(node, w);
      node.addEventListener('click', function () {
        node.classList.toggle('cam-full');
      });
      return node;
    }

    if (w.type === 'rss') {
      var rh = document.createElement('div');
      rh.className = 'name';
      rh.textContent = w.name || 'News';
      var list = document.createElement('div');
      list.className = 'rss';
      node.appendChild(rh);
      node.appendChild(list);
      loadFeed(node, w);
      return node;
    }

    var domain = (w.entity || '').split('.')[0];
    var isAction = !!ACTION_DOMAINS[domain];
    if (isAction) {
      node.classList.add('is-action');
    }

    var n = document.createElement('div');
    n.className = 'name';
    n.textContent = friendly(w);

    var v = document.createElement('div');
    v.className = 'value';
    v.textContent = '—';

    var sub = document.createElement('div');
    sub.className = 'sub';

    node.appendChild(n);
    node.appendChild(v);
    node.appendChild(sub);

    if (isAction) {
      node.addEventListener('click', function () {
        act(w.entity, defaultService(w.entity, entityState(w.entity)));
      });
    } else if (w.type === 'entity' && w.entity) {
      node.addEventListener('click', function () {
        toggle(w);
      });
    }
    return node;
  }

  function paint(node, w, st) {
    var valueEl = node.querySelector('.value');
    var subEl = node.querySelector('.sub');
    var nameEl = node.querySelector('.name') || node.querySelector('.cam-name');
    if (nameEl && !w.name) {
      nameEl.textContent = friendly(w);
    }
    if (!valueEl) {
      return;
    }
    if (!st) {
      valueEl.textContent = '—';
      node.classList.remove('active');
      if (subEl) {
        subEl.textContent = 'нет данных';
      }
      return;
    }

    if (w.type === 'weather') {
      var a = st.attributes || {};
      var temp = a.temperature;
      valueEl.textContent = temp != null ? Math.round(temp) + '°' : '—';
      var parts = [CONDITIONS[st.state] || st.state];
      if (a.humidity != null) {
        parts.push(a.humidity + '%');
      }
      if (a.wind_speed != null) {
        parts.push(a.wind_speed + ' ' + (a.wind_speed_unit || ''));
      }
      if (subEl) {
        subEl.textContent = parts.join(' · ');
      }
      node.classList.remove('active');
      return;
    }

    var v = st.state;
    var domain = (w.entity || '').split('.')[0];
    var unit = '';
    if (st.attributes && st.attributes.unit_of_measurement) {
      unit = ' ' + String(st.attributes.unit_of_measurement);
    }

    if (ACTION_DOMAINS[domain]) {
      valueEl.textContent = friendly(w) || 'Run';
      node.classList.remove('active');
      if (subEl) {
        subEl.textContent = '';
      }
      return;
    }

    if (w.type === 'sensor') {
      valueEl.textContent = v + unit;
      node.classList.remove('active');
      if (subEl) {
        subEl.textContent = '';
      }
      return;
    }

    if (v === 'on') {
      valueEl.textContent = 'On';
    } else if (v === 'off') {
      valueEl.textContent = 'Off';
    } else {
      valueEl.textContent = v;
    }

    if (isOn(v)) {
      node.classList.add('active');
    } else {
      node.classList.remove('active');
    }

    if (subEl) {
      if (v === 'unavailable' || v === 'unknown') {
        subEl.textContent = v;
      } else if (st.attributes && st.attributes.current_temperature != null) {
        subEl.textContent = 'сейчас ' + st.attributes.current_temperature + '°';
      } else {
        subEl.textContent = '';
      }
    }
  }

  function render() {
    for (var c = 0; c < camTimers.length; c++) {
      clearInterval(camTimers[c]);
    }
    camTimers = [];
    grid.innerHTML = '';
    tiles = [];
    var ws = cfg.widgets || [];
    for (var i = 0; i < ws.length; i++) {
      var w = ws[i];
      var node = buildTile(w);
      grid.appendChild(node);
      tiles.push({ w: w, node: node });
      if (w.entity) {
        paint(node, w, entityState(w.entity));
      }
    }
  }

  function applyState(id, st) {
    for (var i = 0; i < tiles.length; i++) {
      if (tiles[i].w.entity === id) {
        paint(tiles[i].node, tiles[i].w, st);
      }
    }
  }

  function toggle(w) {
    var st = entityState(w.entity);
    act(w.entity, defaultService(w.entity, st));
  }

  function act(id, service) {
    api('api/action', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ entity_id: id, service: service })
    })
      .then(function (r) {
        if (!r.ok) {
          toast('Не удалось выполнить');
        }
      })
      .catch(function () {
        toast('Не удалось выполнить');
      });
  }

  function loadAlerts() {
    for (var i = 0; i < tiles.length; i++) {
      if (tiles[i].w.type === 'alerts') {
        fetchAlerts(tiles[i]);
      }
    }
  }

  function fetchAlerts(t) {
    api('api/alerts?id=' + encodeURIComponent(t.w.id))
      .then(function (r) {
        return r.ok ? r.json() : null;
      })
      .then(function (d) {
        if (d) {
          renderAlerts(t.node, d);
        }
      })
      .catch(function () {});
  }

  function alertRow(text, level) {
    var row = document.createElement('div');
    row.className = 'alert-item';
    var dot = document.createElement('span');
    dot.className = 'alert-dot ' + level;
    var txt = document.createElement('span');
    txt.className = 'alert-text';
    txt.textContent = text;
    row.appendChild(dot);
    row.appendChild(txt);
    return row;
  }

  function renderAlerts(node, d) {
    var box = node.querySelector('.alerts');
    if (!box) {
      return;
    }
    box.innerHTML = '';
    var items = d.items || [];
    if (!items.length) {
      box.appendChild(alertRow('Всё спокойно', 'ok'));
      return;
    }
    for (var i = 0; i < items.length; i++) {
      box.appendChild(alertRow(items[i].text, items[i].level || 'warn'));
    }
    if (d.more) {
      box.appendChild(alertRow('+ ещё ' + d.more, 'more'));
    }
  }

  function loadStates() {    return api('api/state')
      .then(function (r) {
        return r.ok ? r.json() : {};
      })
      .then(function (map) {
        states = map || {};
        for (var i = 0; i < tiles.length; i++) {
          var t = tiles[i];
          if (t.w.entity) {
            paint(t.node, t.w, states[t.w.entity]);
          }
        }
      })
      .catch(function () {});
  }

  function loadConfig() {
    return api('api/config')
      .then(function (r) {
        if (r.status === 401) {
          showNoToken();
          throw new Error('unauthorized');
        }
        if (!r.ok) {
          throw new Error('config ' + r.status);
        }
        return r.json();
      })
      .then(function (c) {
        cfg = c;
        document.title = c.title || 'Home';
        document.body.className = 'theme-' + effectiveTheme(c);
        grid.style.setProperty('--tracks', (c.columns || 6) * 2);
        render();
        loadAlerts();
        return loadStates().then(function () {
          return true;
        });
      })
      .catch(function (e) {
        if (String(e.message) !== 'unauthorized') {
          toast('Нет связи с сервером');
        }
        return false;
      });
  }

  function connect() {
    if (!token) {
      return;
    }
    if (es) {
      es.close();
    }
    es = new EventSource('api/events' + (token ? '?t=' + encodeURIComponent(token) : ''));
    es.onopen = function () {
      dot.classList.add('on');
      lastEvent = Date.now();
    };
    es.onerror = function () {
      dot.classList.remove('on');
    };
    es.onmessage = function (e) {
      lastEvent = Date.now();
      var d;
      try {
        d = JSON.parse(e.data);
      } catch (err) {
        return;
      }
      if (d.type === 'state' && d.state) {
        states[d.entity_id] = d.state;
        applyState(d.entity_id, d.state);
      } else if (d.type === 'config') {
        loadConfig();
      }
    };
  }

  function startCamera(node, w) {
    var img = node.querySelector('.cam');
    if (!img) {
      return;
    }
    var interval = Math.max(2, w.interval || 3) * 1000;

    function refresh() {
      if (document.hidden) {
        return;
      }
      var loader = new Image();
      loader.onload = function () {
        img.src = loader.src;
      };
      loader.src =
        'api/camera?id=' +
        encodeURIComponent(w.id) +
        '&t=' +
        encodeURIComponent(token) +
        '&v=' +
        Date.now();
    }

    refresh();
    camTimers.push(setInterval(refresh, interval));
  }

  function startClock(node) {
    function tick() {
      var d = new Date();
      var t = node.querySelector('.time');
      var dt = node.querySelector('.date');
      if (t) {
        t.textContent = pad(d.getHours()) + ':' + pad(d.getMinutes());
      }
      if (dt) {
        var text = '';
        try {
          text = d.toLocaleDateString(undefined, {
            weekday: 'long',
            day: 'numeric',
            month: 'long'
          });
        } catch (e) {
          text = d.getDate() + '.' + (d.getMonth() + 1) + '.' + d.getFullYear();
        }
        dt.textContent = text;
      }
    }
    tick();
    setInterval(tick, 15000);
  }

  function shortDate(s) {
    if (!s) {
      return '';
    }
    var d = new Date(s);
    if (isNaN(d.getTime())) {
      return '';
    }
    return pad(d.getDate()) + '.' + pad(d.getMonth() + 1) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  function loadFeed(node, w) {
    var list = node.querySelector('.rss');
    if (!w.feed) {
      list.textContent = 'лента не задана';
      return;
    }

    function refresh() {
      api('api/rss?id=' + encodeURIComponent(w.id))
        .then(function (r) {
          return r.ok ? r.json() : null;
        })
        .then(function (d) {
          if (!d) {
            return;
          }
          list.innerHTML = '';
          var items = d.items || [];
          if (!items.length) {
            list.textContent = 'Пусто';
            return;
          }
          for (var i = 0; i < items.length; i++) {
            var it = items[i];
            var a = document.createElement('a');
            a.className = 'rss-item';
            a.href = it.link || '#';
            a.target = '_blank';
            a.rel = 'noopener noreferrer';

            var title = document.createElement('div');
            title.className = 'rss-title';
            title.textContent = it.title || '(без заголовка)';

            var meta = document.createElement('div');
            meta.className = 'rss-meta';
            meta.textContent = [it.source, shortDate(it.published)].filter(Boolean).join(' · ');

            if (w.images && it.image) {
              var img = document.createElement('img');
              img.className = 'rss-img';
              img.alt = '';
              img.src = it.image;
              img.onerror = function () {
                this.style.display = 'none';
              };
              a.appendChild(img);
            }

            var text = document.createElement('div');
            text.className = 'rss-text';
            text.appendChild(title);
            text.appendChild(meta);
            a.appendChild(text);
            list.appendChild(a);
          }
        })
        .catch(function () {});
    }

    refresh();
    setInterval(refresh, 10 * 60 * 1000);
  }

  function showNoToken() {
    if (noTokenShown) {
      return;
    }
    noTokenShown = true;
    var o = document.createElement('div');
    o.className = 'overlay';
    var p = document.createElement('div');
    p.className = 'panel';
    var h = document.createElement('h1');
    h.textContent = 'Нужен токен';
    var t = document.createElement('p');
    t.textContent = 'Откройте админку (панель Old Tablet в Home Assistant) и скопируйте ссылку для планшета. В ссылке должен быть параметр ?t=…';
    p.appendChild(h);
    p.appendChild(t);
    o.appendChild(p);
    document.body.appendChild(o);
  }

  loadConfig().then(function (ok) {
    if (ok) {
      connect();
    }
  });

  setInterval(function () {
    if (!reloading && Date.now() - lastEvent > 300000) {
      reloading = true;
      location.reload();
    }
  }, 30000);

  setInterval(function () {
    var d = new Date();
    if (!reloading && d.getHours() === 4 && d.getMinutes() < 2) {
      reloading = true;
      location.reload();
    }
  }, 60000);

  document.addEventListener('visibilitychange', function () {
    if (!document.hidden && cfg && Date.now() - lastEvent > 60000) {
      connect();
      loadStates();
      loadAlerts();
      lastEvent = Date.now();
    }
  });

  setInterval(loadAlerts, 30000);
  setInterval(function () {
    if (cfg && cfg.theme_mode === 'time') {
      document.body.className = 'theme-' + effectiveTheme(cfg);
    }
  }, 60000);
})();
