(function () {
  'use strict';

  var DEVICES = {
    'ipad-9.7': { name: 'iPad 9.7" · 2048×1536', ratio: 4 / 3, cols: 6 },
    'ipad-10.2': { name: 'iPad 10.2" · 2160×1620', ratio: 4 / 3, cols: 6 },
    'ipad-10.9': { name: 'iPad 10.9" · 2360×1640', ratio: 2360 / 1640, cols: 7 },
    'ipad-12.9': { name: 'iPad 12.9" · 2732×2048', ratio: 4 / 3, cols: 8 },
    'custom': { name: 'Свои пропорции · 4:3', ratio: 4 / 3, cols: 6 }
  };

  var cfg = null;
  var states = [];
  var selected = -1;
  var dirty = false;
  var dragIndex = -1;
  var dragged = false;
  var resizing = false;

  var ACTION_DOMAINS = { script: true, scene: true, button: true, input_button: true };

  function $(id) {
    return document.getElementById(id);
  }

  function clamp(v, min, max) {
    return v < min ? min : v > max ? max : v;
  }

  function req(method, path, body) {
    var opts = { method: method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (r) {
      if (!r.ok) {
        throw new Error(path + ': ' + r.status);
      }
      return r.json();
    });
  }

  function load() {
    return Promise.all([
      req('GET', 'api/admin/config'),
      req('GET', 'api/admin/status'),
      req('GET', 'api/admin/states')
    ])
      .then(function (res) {
        cfg = res[0];
        states = res[2] || [];
        if (!cfg.widgets) {
          cfg.widgets = [];
        }
        if (!cfg.theme) {
          cfg.theme = 'dark';
        }
        if (!cfg.columns) {
          cfg.columns = 6;
        }
        if (!cfg.device) {
          cfg.device = 'ipad-9.7';
        }
        selected = cfg.widgets.length ? 0 : -1;

        if (!$('host').value) {
          $('host').value = location.hostname || 'homeassistant.local';
        }
        fillDevices();
        fillEntityList();
        syncToolbar();
        renderStatus(res[1]);
        renderTablet();
        renderAll();
      })
      .catch(function (e) {
        $('ha-status').textContent = 'ошибка: ' + e.message;
      });
  }

  function fillEntityList() {
    var dl = $('entity-list');
    dl.innerHTML = '';
    for (var i = 0; i < states.length; i++) {
      var o = document.createElement('option');
      o.value = states[i].entity_id;
      o.label = states[i].name + ' — ' + states[i].state;
      dl.appendChild(o);
    }
  }

  function fillDevices() {
    var sel = $('device');
    sel.innerHTML = '';
    Object.keys(DEVICES).forEach(function (key) {
      var o = document.createElement('option');
      o.value = key;
      o.textContent = DEVICES[key].name;
      sel.appendChild(o);
    });
  }

  function syncToolbar() {
    $('device').value = cfg.device;
    $('columns').value = cfg.columns;
    $('theme').value = cfg.theme;
    $('title').value = cfg.title || '';
  }

  function renderStatus(st) {
    $('ha-dot').className = 'dot' + (st.connected ? ' on' : '');
    $('ha-status').textContent = st.connected ? 'Home Assistant подключён' : 'нет связи с Home Assistant';
    $('ha-entities').textContent = st.connected ? 'сущностей: ' + st.entities : '';
    $('cfg-version').textContent = 'конфиг v' + st.version;
  }

  function tabletHost() {
    var h = ($('host').value || '')
      .replace(/^https?:\/\//, '')
      .replace(/\/.*$/, '')
      .replace(/:.*$/, '')
      .trim();
    return h || 'homeassistant.local';
  }

  function renderTablet() {
    var host = tabletHost();
    $('token').textContent = cfg.device_token || '—';
    if (!cfg.device_token) {
      $('tablet-url').textContent = '—';
      $('qr').removeAttribute('src');
      return;
    }
    $('tablet-url').textContent = 'http://' + host + ':8088/?t=' + cfg.device_token;
    $('qr').src =
      'api/admin/qr?host=' +
      encodeURIComponent(host) +
      '&v=' +
      encodeURIComponent(cfg.device_token.slice(0, 8));
  }

  function autoName(w) {
    if (w.name) {
      return w.name;
    }
    var info = entityInfo(w.entity);
    if (info && info.name) {
      return info.name;
    }
    return w.entity || w.type;
  }

  function entityInfo(id) {
    for (var i = 0; i < states.length; i++) {
      if (states[i].entity_id === id) {
        return states[i];
      }
    }
    return null;
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

  function applyCanvasTheme() {
    $('canvas').className = 'canvas theme-' + (cfg.theme === 'light' ? 'light' : 'dark');
    $('canvas').style.setProperty('--tracks', cfg.columns * 2);
  }

  function fitStage() {
    var d = DEVICES[cfg.device] || DEVICES['ipad-9.7'];
    var width = $('stage').clientWidth;
    if (width > 0) {
      $('stage').style.height = Math.round(width / d.ratio) + 'px';
    }
  }

  function rerenderCanvas() {
    var sc = $('canvas').scrollTop;
    renderCanvas();
    $('canvas').scrollTop = sc;
  }

  function renderCanvas() {
    var canvas = $('canvas');
    canvas.innerHTML = '';
    tileNodes = [];
    canvas.style.setProperty('--tracks', cfg.columns * 2);

    if (!cfg.widgets.length) {
      var empty = document.createElement('div');
      empty.className = 'empty';
      empty.textContent = 'Пусто. Нажмите «+ Виджет».';
      canvas.appendChild(empty);
      return;
    }

    for (var i = 0; i < cfg.widgets.length; i++) {
      var node = makeTile(cfg.widgets[i], i);
      canvas.appendChild(node);
      tileNodes.push(node);
    }
  }

  var tileNodes = [];
  var dragOffTracks = 0;
  var dragOffRows = 0;
  var dragOrigX = 0;
  var dragOrigY = 0;

  function spanOf(v) {
    return Math.max(1, Math.round((v || 1) * 2));
  }

  function place(el, w) {
    var tracks = cfg.columns * 2;
    var span = Math.min(tracks, spanOf(w.w));
    var x = Math.max(0, Math.min(w.x || 0, tracks - span));
    el.style.gridColumn = x + 1 + ' / span ' + span;
    var h = Math.max(1, Math.min(6, w.h || 1));
    el.style.gridRow = (w.y || 0) + 1 + ' / span ' + h;
  }

  function geometry() {
    var canvas = $('canvas');
    var cs = getComputedStyle(canvas);
    var gap = parseFloat(cs.columnGap) || 9;
    var rowGap = parseFloat(cs.rowGap) || gap;
    var padL = parseFloat(cs.paddingLeft) || 0;
    var padT = parseFloat(cs.paddingTop) || 0;
    var tracks = cfg.columns * 2;
    var inner = canvas.clientWidth - padL - (parseFloat(cs.paddingRight) || 0) - gap * (tracks - 1);
    return {
      gap: gap,
      rowGap: rowGap,
      padL: padL,
      padT: padT,
      tracks: tracks,
      trackW: Math.max(1, inner / tracks),
      rowH: 92
    };
  }

  function rectOf(w) {
    return { x: w.x || 0, y: w.y || 0, w: spanOf(w.w), h: Math.max(1, Math.min(6, w.h || 1)) };
  }

  function rectOverlap(a, b) {
    return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
  }

  function overlaps(i) {
    var a = rectOf(cfg.widgets[i]);
    for (var k = 0; k < cfg.widgets.length; k++) {
      if (k === i) {
        continue;
      }
      if (rectOverlap(a, rectOf(cfg.widgets[k]))) {
        return true;
      }
    }
    return false;
  }

  function freeSpot(span, h) {
    var tracks = cfg.columns * 2;
    for (var y = 0; y < 80; y++) {
      for (var x = 0; x <= tracks - span; x++) {
        var r = { x: x, y: y, w: span, h: h };
        var free = true;
        for (var i = 0; i < cfg.widgets.length; i++) {
          if (rectOverlap(r, rectOf(cfg.widgets[i]))) {
            free = false;
            break;
          }
        }
        if (free) {
          return { x: x, y: y };
        }
      }
    }
    return { x: 0, y: 0 };
  }

  function packAll() {
    var tracks = cfg.columns * 2;
    var x = 0;
    var y = 0;
    var rowH = 0;
    for (var i = 0; i < cfg.widgets.length; i++) {
      var w = cfg.widgets[i];
      var span = spanOf(w.w);
      if (x + span > tracks) {
        x = 0;
        y += rowH;
        rowH = 0;
      }
      w.x = x;
      w.y = y;
      x += span;
      if ((w.h || 1) > rowH) {
        rowH = w.h || 1;
      }
    }
    markDirty();
    renderAll();
  }

  function makeTile(w, i) {
    var tile = document.createElement('div');
    tile.className = 'tile t-' + (w.type || 'entity');
    place(tile, w);
    tile.setAttribute('draggable', 'true');
    if (i === selected) {
      tile.classList.add('selected');
    }

    if (w.type === 'spacer') {
      var sp = document.createElement('div');
      sp.className = 'spacer-prev';
      sp.textContent = 'пусто';
      tile.appendChild(sp);
    } else if (w.type === 'clock') {
      var cn = document.createElement('div');
      cn.className = 'name';
      cn.textContent = w.name || '';
      var cv = document.createElement('div');
      cv.className = 'value time';
      var cd = document.createElement('div');
      cd.className = 'sub date';
      tile.appendChild(cn);
      tile.appendChild(cv);
      tile.appendChild(cd);
    } else if (w.type === 'weather') {
      var winfo = entityInfo(w.entity);
      var wn = document.createElement('div');
      wn.className = 'name';
      wn.textContent = autoName(w);
      var wv = document.createElement('div');
      wv.className = 'value';
      wv.textContent = winfo ? winfo.state : '—';
      var ws = document.createElement('div');
      ws.className = 'sub';
      ws.textContent = 'погода';
      tile.appendChild(wn);
      tile.appendChild(wv);
      tile.appendChild(ws);
    } else if (w.type === 'alerts') {
      var an = document.createElement('div');
      an.className = 'name';
      an.textContent = w.name || 'Алерты';
      var ap = document.createElement('div');
      ap.className = 'alerts-prev';
      ap.textContent = 'алерты из Home Assistant';
      tile.appendChild(an);
      tile.appendChild(ap);
    } else if (w.type === 'camera') {
      var cp = document.createElement('div');
      cp.className = 'cam-prev';
      cp.textContent = (w.name || w.entity || 'камера') + ' · снимок ' + (w.interval || 3) + ' с';
      tile.appendChild(cp);
    } else if (w.type === 'rss') {
      var rn = document.createElement('div');
      rn.className = 'name';
      rn.textContent = w.name || 'News';
      var list = document.createElement('div');
      list.className = 'rss-prev';
      var src = document.createElement('div');
      src.className = 'sub';
      try {
        src.textContent = new URL(w.feed).hostname;
      } catch (e) {
        src.textContent = 'укажите ленту';
      }
      list.appendChild(src);
      for (var k = 0; k < 3; k++) {
        var line = document.createElement('div');
        line.className = 'rss-line';
        list.appendChild(line);
      }
      tile.appendChild(rn);
      tile.appendChild(list);
    } else {
      var info = entityInfo(w.entity);
      var state = info ? info.state : '—';
      var domain = (w.entity || '').split('.')[0];
      var isAction = !!ACTION_DOMAINS[domain];
      var name = document.createElement('div');
      name.className = 'name';
      name.textContent = autoName(w);
      var value = document.createElement('div');
      value.className = 'value';
      if (isAction) {
        value.textContent = 'Run';
      } else if (w.type === 'sensor') {
        value.textContent = state;
      } else if (state === 'on') {
        value.textContent = 'On';
      } else if (state === 'off') {
        value.textContent = 'Off';
      } else {
        value.textContent = state;
      }
      var sub = document.createElement('div');
      sub.className = 'sub';
      sub.textContent = w.type === 'sensor' ? '' : w.entity || '';
      if (!isAction && isOn(state)) {
        tile.classList.add('active');
      }
      tile.appendChild(name);
      tile.appendChild(value);
      tile.appendChild(sub);
    }

    tile.addEventListener('click', function () {
      if (!dragged) {
        select(i);
      }
    });

    tile.addEventListener('dragstart', function (e) {
      if (resizing) {
        e.preventDefault();
        return;
      }
      dragged = true;
      dragIndex = i;
      var dw = cfg.widgets[i];
      dragOrigX = dw.x || 0;
      dragOrigY = dw.y || 0;
      var g = geometry();
      var r = tile.getBoundingClientRect();
      dragOffTracks = (e.clientX - r.left) / (g.trackW + g.gap);
      dragOffRows = (e.clientY - r.top) / (g.rowH + g.rowGap);
      try {
        e.dataTransfer.setData('text/plain', String(i));
      } catch (err) {
        /* ignore */
      }
      e.dataTransfer.effectAllowed = 'move';
      tile.classList.add('dragging');
    });

    tile.addEventListener('dragend', function () {
      finishDrag(false);
    });

    var handle = document.createElement('div');
    handle.className = 'resize';
    handle.setAttribute('draggable', 'false');
    handle.addEventListener('mousedown', function (e) {
      startResize(e, i);
    });
    handle.addEventListener(
      'touchstart',
      function (e) {
        startResize(e, i);
      },
      { passive: false }
    );
    tile.appendChild(handle);

    return tile;
  }

  function finishDrag(dropped) {
    if (dragIndex < 0) {
      return;
    }
    var i = dragIndex;
    var w = cfg.widgets[i];
    dragIndex = -1;
    if (!w) {
      return;
    }
    if (tileNodes[i]) {
      tileNodes[i].classList.remove('dragging');
      tileNodes[i].classList.remove('overlap');
    }
    if (!dropped || overlaps(i)) {
      w.x = dragOrigX;
      w.y = dragOrigY;
      if (tileNodes[i]) {
        place(tileNodes[i], w);
      }
    } else if (w.x !== dragOrigX || w.y !== dragOrigY) {
      markDirty();
    }
    setTimeout(function () {
      dragged = false;
    }, 0);
  }

  function select(i) {
    selected = i;
    for (var k = 0; k < tileNodes.length; k++) {
      if (tileNodes[k]) {
        tileNodes[k].classList.toggle('selected', k === i);
      }
    }
    renderInspector();
  }

  function renderInspector() {
    var has = selected >= 0 && selected < cfg.widgets.length;
    $('fields').hidden = !has;
    $('empty-inspector').hidden = has;
    if (!has) {
      return;
    }
    var w = cfg.widgets[selected];
    setVal('e-type', w.type || 'entity');
    setVal('e-entity', w.entity || '');
    setVal('e-name', w.name || '');
    setVal('e-feed', w.feed || '');
    setVal('e-limit', w.limit || 8);
    setVal('e-interval', w.interval || 3);
    setVal('e-w', w.w || 1);
    setVal('e-h', w.h || 1);
    applyFieldState();
  }

  function setVal(id, v) {
    var el = $(id);
    if (el.value !== String(v)) {
      el.value = v;
    }
  }

  function applyFieldState() {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    $('e-entity').disabled = w.type === 'rss' || w.type === 'clock' || w.type === 'spacer';
    $('e-entity').placeholder = w.type === 'alerts' ? 'необязательно: sensor.alerts' : 'light.kitchen';
    $('e-feed').disabled = w.type !== 'rss';
    $('e-limit').disabled = w.type !== 'rss';
    $('e-interval').disabled = w.type !== 'camera';
    $('e-w').max = cfg.columns;
  }

  function startResize(ev, i) {
    if (ev.type === 'mousedown' && ev.button !== 0) {
      return;
    }
    ev.preventDefault();
    ev.stopPropagation();
    select(i);

    var w = cfg.widgets[i];
    var tile = tileNodes[i] || ev.currentTarget.parentNode;
    var touch = ev.type === 'touchstart';
    var p0 = touch ? ev.touches[0] : ev;
    var startX = p0.clientX;
    var startY = p0.clientY;
    var startSpan = spanOf(w.w);
    var startH = w.h || 1;
    var origW = w.w;
    var origH = w.h;

    var g = geometry();
    resizing = true;

    function move(e) {
      var p = touch ? (e.touches && e.touches[0]) : e;
      if (!p) {
        return;
      }
      if (e.cancelable) {
        e.preventDefault();
      }
      var dSpan = Math.round((p.clientX - startX) / (g.trackW + g.gap));
      var dH = Math.round((p.clientY - startY) / (g.rowH + g.rowGap));
      var span = clamp(startSpan + dSpan, 1, g.tracks - (w.x || 0));
      var h = clamp(startH + dH, 1, 6);
      if (span !== spanOf(w.w) || h !== w.h) {
        w.w = span / 2;
        w.h = h;
        place(tile, w);
        tile.classList.toggle('overlap', overlaps(i));
        $('e-w').value = w.w;
        $('e-h').value = h;
        markDirty();
      }
    }

    function up() {
      resizing = false;
      document.removeEventListener('mousemove', move);
      document.removeEventListener('mouseup', up);
      document.removeEventListener('touchmove', move);
      document.removeEventListener('touchend', up);
      if (overlaps(i)) {
        w.w = origW;
        w.h = origH;
        place(tile, w);
        tile.classList.remove('overlap');
      }
      rerenderCanvas();
      renderInspector();
    }

    if (touch) {
      document.addEventListener('touchmove', move, { passive: false });
      document.addEventListener('touchend', up);
    } else {
      document.addEventListener('mousemove', move);
      document.addEventListener('mouseup', up);
    }
  }

  function markDirty() {
    dirty = true;
    $('dirty').textContent = 'не сохранено';
  }

  function clearDirty() {
    dirty = false;
    $('dirty').textContent = '';
  }

  function addWidget() {
    var spot = freeSpot(spanOf(2), 1);
    cfg.widgets.push({ id: '', type: 'entity', entity: '', name: '', interval: 3, x: spot.x, y: spot.y, w: 2, h: 1 });
    selected = cfg.widgets.length - 1;
    markDirty();
    renderAll();
  }

  function duplicateWidget() {
    if (selected < 0) {
      return;
    }
    var copy = JSON.parse(JSON.stringify(cfg.widgets[selected]));
    copy.id = '';
    var spot = freeSpot(spanOf(copy.w), copy.h || 1);
    copy.x = spot.x;
    copy.y = spot.y;
    cfg.widgets.splice(selected + 1, 0, copy);
    selected = selected + 1;
    markDirty();
    renderAll();
  }

  function deleteWidget() {
    if (selected < 0) {
      return;
    }
    cfg.widgets.splice(selected, 1);
    if (selected >= cfg.widgets.length) {
      selected = cfg.widgets.length - 1;
    }
    markDirty();
    renderAll();
  }

  function renderAll() {
    applyCanvasTheme();
    rerenderCanvas();
    renderInspector();
    fitStage();
  }

  function save() {
    $('save').disabled = true;
    var body = {
      title: cfg.title || '',
      theme: cfg.theme,
      columns: cfg.columns,
      device: cfg.device,
      layout_version: 2,
      widgets: cfg.widgets
    };
    req('PUT', 'api/admin/config', body)
      .then(function (saved) {
        cfg = saved;
        if (!cfg.widgets) {
          cfg.widgets = [];
        }
        if (selected >= cfg.widgets.length) {
          selected = cfg.widgets.length - 1;
        }
        clearDirty();
        syncToolbar();
        renderTablet();
        renderAll();
        return req('GET', 'api/admin/status');
      })
      .then(renderStatus)
      .catch(function (e) {
        $('dirty').textContent = 'ошибка: ' + e.message;
      })
      .then(function () {
        $('save').disabled = false;
      });
  }

  function regen() {
    if (!confirm('Старый токен перестанет работать. Планшет придётся открыть по новой ссылке. Продолжить?')) {
      return;
    }
    req('POST', 'api/admin/token')
      .then(function (res) {
        cfg.device_token = res.token;
        renderTablet();
      })
      .catch(function (e) {
        alert('Не удалось: ' + e.message);
      });
  }

  function tickClocks() {
    var d = new Date();
    var hh = (d.getHours() < 10 ? '0' : '') + d.getHours();
    var mm = (d.getMinutes() < 10 ? '0' : '') + d.getMinutes();
    var times = $('canvas').querySelectorAll('.time');
    for (var i = 0; i < times.length; i++) {
      times[i].textContent = hh + ':' + mm;
    }
    var dates = $('canvas').querySelectorAll('.t-clock .date');
    for (var j = 0; j < dates.length; j++) {
      try {
        dates[j].textContent = d.toLocaleDateString(undefined, {
          weekday: 'long',
          day: 'numeric',
          month: 'long'
        });
      } catch (e) {
        dates[j].textContent = d.getDate() + '.' + (d.getMonth() + 1) + '.' + d.getFullYear();
      }
    }
  }

  $('save').addEventListener('click', save);
  $('add').addEventListener('click', addWidget);
  $('pack').addEventListener('click', packAll);
  $('regen').addEventListener('click', regen);
  $('host').addEventListener('change', renderTablet);

  var canvasEl = $('canvas');
  canvasEl.addEventListener('dragover', function (e) {
    if (dragIndex < 0) {
      return;
    }
    e.preventDefault();
    var w = cfg.widgets[dragIndex];
    if (!w) {
      return;
    }
    var g = geometry();
    var rect = canvasEl.getBoundingClientRect();
    var px = e.clientX - rect.left - g.padL + canvasEl.scrollLeft;
    var py = e.clientY - rect.top - g.padT + canvasEl.scrollTop;
    var span = spanOf(w.w);
    var tx = Math.round(px / (g.trackW + g.gap) - dragOffTracks);
    var ty = Math.round(py / (g.rowH + g.rowGap) - dragOffRows);
    tx = clamp(tx, 0, g.tracks - span);
    ty = Math.max(0, ty);
    if (tx !== w.x || ty !== w.y) {
      w.x = tx;
      w.y = ty;
      if (tileNodes[dragIndex]) {
        place(tileNodes[dragIndex], w);
        tileNodes[dragIndex].classList.toggle('overlap', overlaps(dragIndex));
      }
    }
  });
  canvasEl.addEventListener('drop', function (e) {
    e.preventDefault();
    finishDrag(true);
  });
  $('e-dup').addEventListener('click', duplicateWidget);
  $('e-del').addEventListener('click', deleteWidget);

  $('device').addEventListener('change', function () {
    cfg.device = this.value;
    var d = DEVICES[cfg.device];
    if (d && cfg.device !== 'custom') {
      cfg.columns = d.cols;
      $('columns').value = d.cols;
    }
    markDirty();
    applyCanvasTheme();
    renderAll();
  });

  $('columns').addEventListener('change', function () {
    cfg.columns = clamp(parseInt(this.value, 10) || 6, 2, 12);
    this.value = cfg.columns;
    var tracks = cfg.columns * 2;
    for (var i = 0; i < cfg.widgets.length; i++) {
      var w = cfg.widgets[i];
      var span = Math.min(spanOf(w.w), tracks);
      w.w = span / 2;
      if ((w.x || 0) + span > tracks) {
        w.x = Math.max(0, tracks - span);
      }
    }
    markDirty();
    renderAll();
  });

  $('theme').addEventListener('change', function () {
    cfg.theme = this.value === 'light' ? 'light' : 'dark';
    markDirty();
    applyCanvasTheme();
  });

  $('title').addEventListener('input', function () {
    cfg.title = this.value;
    markDirty();
  });

  $('e-type').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.type = this.value;
    markDirty();
    applyFieldState();
    rerenderCanvas();
  });

  $('e-entity').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.entity = this.value.trim();
    markDirty();
    rerenderCanvas();
  });

  $('e-name').addEventListener('input', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.name = this.value;
    markDirty();
    var node = $('canvas').children[selected];
    var n = node && node.querySelector ? node.querySelector('.name') : null;
    if (n) {
      n.textContent = autoName(w);
    }
  });

  $('e-feed').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.feed = this.value.trim();
    markDirty();
    rerenderCanvas();
  });

  $('e-limit').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.limit = clamp(parseInt(this.value, 10) || 8, 3, 30);
    this.value = w.limit;
    markDirty();
  });

  $('e-interval').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.interval = clamp(parseInt(this.value, 10) || 3, 2, 30);
    this.value = w.interval;
    markDirty();
    rerenderCanvas();
  });

  $('e-w').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    var v = parseFloat(this.value);
    if (isNaN(v)) {
      v = 1;
    }
    w.w = Math.round(clamp(v, 0.5, cfg.columns) * 2) / 2;
    this.value = w.w;
    markDirty();
    rerenderCanvas();
  });

  $('e-newrow') && $('e-newrow').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.new_row = this.checked;
    markDirty();
    rerenderCanvas();
  });

  $('e-h').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.h = clamp(parseInt(this.value, 10) || 1, 1, 6);
    this.value = w.h;
    markDirty();
    rerenderCanvas();
  });

  window.addEventListener('resize', fitStage);
  window.addEventListener('beforeunload', function (e) {
    if (dirty) {
      e.preventDefault();
      e.returnValue = '';
    }
  });

  setInterval(tickClocks, 15000);
  setInterval(function () {
    req('GET', 'api/admin/status').then(renderStatus).catch(function () {});
  }, 10000);

  load().then(tickClocks);
})();
