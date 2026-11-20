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
    $('canvas').style.setProperty('--cols', cfg.columns);
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
    canvas.style.setProperty('--cols', cfg.columns);

    if (!cfg.widgets.length) {
      var empty = document.createElement('div');
      empty.className = 'empty';
      empty.textContent = 'Пусто. Нажмите «+ Виджет».';
      canvas.appendChild(empty);
      return;
    }

    for (var i = 0; i < cfg.widgets.length; i++) {
      canvas.appendChild(makeTile(cfg.widgets[i], i));
    }
  }

  function makeTile(w, i) {
    var tile = document.createElement('div');
    tile.className = 'tile t-' + (w.type || 'entity');
    tile.style.gridColumn = 'span ' + Math.min(w.w || 1, cfg.columns);
    tile.style.gridRow = 'span ' + Math.min(w.h || 1, 6);
    tile.setAttribute('draggable', 'true');
    if (i === selected) {
      tile.classList.add('selected');
    }

    if (w.type === 'clock') {
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
      var name = document.createElement('div');
      name.className = 'name';
      name.textContent = autoName(w);
      var value = document.createElement('div');
      value.className = 'value';
      if (w.type === 'sensor') {
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
      if (isOn(state)) {
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
      try {
        e.dataTransfer.setData('text/plain', String(i));
      } catch (err) {
        /* ignore */
      }
      e.dataTransfer.effectAllowed = 'move';
      tile.classList.add('dragging');
    });

    tile.addEventListener('dragend', function () {
      tile.classList.remove('dragging');
      dragIndex = -1;
      clearDropMarks();
      setTimeout(function () {
        dragged = false;
      }, 0);
    });

    tile.addEventListener('dragover', function (e) {
      if (dragIndex < 0 || dragIndex === i) {
        return;
      }
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      tile.classList.add('drop');
    });

    tile.addEventListener('dragleave', function () {
      tile.classList.remove('drop');
    });

    tile.addEventListener('drop', function (e) {
      e.preventDefault();
      tile.classList.remove('drop');
      if (dragIndex >= 0 && dragIndex !== i) {
        moveWidget(dragIndex, i);
      }
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

  function clearDropMarks() {
    var nodes = $('canvas').querySelectorAll('.drop');
    for (var i = 0; i < nodes.length; i++) {
      nodes[i].classList.remove('drop');
    }
  }

  function moveWidget(from, to) {
    var item = cfg.widgets.splice(from, 1)[0];
    cfg.widgets.splice(to, 0, item);
    selected = to;
    markDirty();
    rerenderCanvas();
    renderInspector();
  }

  function select(i) {
    selected = i;
    var nodes = $('canvas').children;
    for (var k = 0; k < nodes.length; k++) {
      if (nodes[k].classList && nodes[k].classList.contains('tile')) {
        nodes[k].classList.toggle('selected', k === i);
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
    $('e-entity').disabled = w.type === 'rss' || w.type === 'clock';
    $('e-feed').disabled = w.type !== 'rss';
    $('e-limit').disabled = w.type !== 'rss';
  }

  function startResize(ev, i) {
    if (ev.type === 'mousedown' && ev.button !== 0) {
      return;
    }
    ev.preventDefault();
    ev.stopPropagation();
    select(i);

    var w = cfg.widgets[i];
    var tile = ev.currentTarget.parentNode;
    var touch = ev.type === 'touchstart';
    var p0 = touch ? ev.touches[0] : ev;
    var startX = p0.clientX;
    var startY = p0.clientY;
    var startW = w.w || 1;
    var startH = w.h || 1;

    var canvas = $('canvas');
    var cs = getComputedStyle(canvas);
    var gap = parseFloat(cs.columnGap) || 9;
    var pad = (parseFloat(cs.paddingLeft) || 0) + (parseFloat(cs.paddingRight) || 0);
    var cellW = (canvas.clientWidth - pad - gap * (cfg.columns - 1)) / cfg.columns + gap;
    var rowH = 92 + gap;

    resizing = true;

    function move(e) {
      var p = touch ? (e.touches && e.touches[0]) : e;
      if (!p) {
        return;
      }
      if (e.cancelable) {
        e.preventDefault();
      }
      var nw = clamp(startW + Math.round((p.clientX - startX) / cellW), 1, cfg.columns);
      var nh = clamp(startH + Math.round((p.clientY - startY) / rowH), 1, 6);
      if (nw !== w.w || nh !== w.h) {
        w.w = nw;
        w.h = nh;
        tile.style.gridColumn = 'span ' + nw;
        tile.style.gridRow = 'span ' + nh;
        $('e-w').value = nw;
        $('e-h').value = nh;
        markDirty();
      }
    }

    function up() {
      resizing = false;
      document.removeEventListener('mousemove', move);
      document.removeEventListener('mouseup', up);
      document.removeEventListener('touchmove', move);
      document.removeEventListener('touchend', up);
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
    cfg.widgets.push({ id: '', type: 'entity', entity: '', name: '', w: 2, h: 1 });
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
  $('regen').addEventListener('click', regen);
  $('host').addEventListener('change', renderTablet);
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

  $('e-w').addEventListener('change', function () {
    var w = cfg.widgets[selected];
    if (!w) {
      return;
    }
    w.w = clamp(parseInt(this.value, 10) || 1, 1, cfg.columns);
    this.value = w.w;
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
