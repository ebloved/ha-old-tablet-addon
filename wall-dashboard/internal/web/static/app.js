(function () {
  'use strict';

  var TOKEN_KEY = 'otd_token';

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

  function api(path, opts) {
    opts = opts || {};
    opts.headers = opts.headers || {};
    opts.headers['X-Dash-Token'] = token;
    return fetch(path, opts);
  }

  function pad(n) {
    return n < 10 ? '0' + n : '' + n;
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
    if (w.w) {
      node.style.gridColumn = 'span ' + Math.min(6, w.w);
    }
    if (w.h) {
      node.style.gridRow = 'span ' + Math.min(4, w.h);
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

    if (w.type === 'entity' && w.entity) {
      node.addEventListener('click', function () {
        toggle(w);
      });
    }
    return node;
  }

  function paint(node, w, st) {
    var valueEl = node.querySelector('.value');
    var subEl = node.querySelector('.sub');
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

    var v = st.state;
    var unit = '';
    if (st.attributes && st.attributes.unit_of_measurement) {
      unit = ' ' + String(st.attributes.unit_of_measurement);
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
    var svc = 'toggle';
    if (w.entity.indexOf('cover.') === 0) {
      svc = st && (st.state === 'open' || st.state === 'opening') ? 'close_cover' : 'open_cover';
    }
    act(w.entity, svc);
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

  function loadStates() {
    return api('api/state')
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
        render();
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

            a.appendChild(title);
            a.appendChild(meta);
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
      lastEvent = Date.now();
    }
  });
})();
