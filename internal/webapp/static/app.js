'use strict';

(function () {
  var tg = window.Telegram && window.Telegram.WebApp;
  if (tg) { tg.ready(); tg.expand(); }

  // --- State ---
  var menu = [];
  var cart = {};          // sku -> quantity
  var activeCategory = '';
  var selectedPickup = 0;
  var pollTimer = null;
  var currentOrderID = null;

  // --- API helpers ---
  function api(method, path, body) {
    var opts = { method: method, headers: {} };
    if (body) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (r) {
      if (!r.ok) return r.json().then(function (e) { throw new Error(e.error || 'Request failed'); });
      return r.json();
    });
  }

  // --- Auth ---
  function authenticate() {
    if (!tg || !tg.initData) {
      // Dev mode: skip auth if not in Telegram.
      return Promise.resolve();
    }
    return api('POST', '/api/auth', { init_data: tg.initData }).catch(function (e) {
      console.error('auth failed:', e);
    });
  }

  // --- Format ---
  function fmt(cents) {
    return '$' + (cents / 100).toFixed(2);
  }

  // --- Screen navigation ---
  function show(id) {
    var screens = document.querySelectorAll('.screen');
    for (var i = 0; i < screens.length; i++) screens[i].classList.add('hidden');
    document.getElementById(id).classList.remove('hidden');
  }

  function showMenu() { show('screen-menu'); }
  function showCart() { renderCart(); show('screen-cart'); }
  function showPayment() { show('screen-payment'); }
  function showSuccess() { show('screen-success'); }
  function showError(msg) {
    document.getElementById('error-message').textContent = msg || 'Please try again.';
    show('screen-error');
  }

  function showClosed() {
    show('screen-closed');
  }

  // --- Menu rendering ---
  function loadMenu() {
    api('GET', '/api/menu').then(function (resp) {
      if (!resp.shop_open) {
        showShopClosed();
        return;
      }
      menu = resp.items;
      var cats = {};
      for (var i = 0; i < menu.length; i++) {
        if (!cats[menu[i].category]) cats[menu[i].category] = true;
      }
      var catList = Object.keys(cats);
      activeCategory = catList[0] || '';
      renderCategories(catList);
      renderMenu();
    }).catch(function (e) {
      showError('Failed to load menu');
    });
  }

  function showShopClosed() {
    showClosed();
  }

  function renderCategories(cats) {
    var el = document.getElementById('categories');
    el.innerHTML = '';
    cats.forEach(function (cat) {
      var btn = document.createElement('button');
      btn.className = 'cat-btn' + (cat === activeCategory ? ' active' : '');
      btn.textContent = cat;
      btn.onclick = function () {
        activeCategory = cat;
        renderCategories(cats);
        renderMenu();
      };
      el.appendChild(btn);
    });
  }

  function renderMenu() {
    var el = document.getElementById('menu-items');
    el.innerHTML = '';
    menu.filter(function (m) { return m.category === activeCategory; }).forEach(function (m) {
      var card = document.createElement('div');
      card.className = 'menu-card';
      card.innerHTML =
        '<div class="item-name">' + esc(m.name) + '</div>' +
        '<div class="item-price">' + fmt(m.price_cents) + '</div>' +
        '<div id="card-btn-' + m.sku + '"></div>';
      el.appendChild(card);
      renderCardButton(m.sku);
    });
  }

  function renderCardButton(sku) {
    var container = document.getElementById('card-btn-' + sku);
    if (!container) return;
    var qty = cart[sku] || 0;
    if (qty === 0) {
      container.innerHTML = '<button class="add-btn" data-act="add">+ Add</button>';
      container.querySelector('[data-act="add"]').onclick = function () { addToCart(sku); };
    } else {
      container.innerHTML =
        '<div class="qty-control">' +
        '<button class="qty-btn" data-act="dec">\u2212</button>' +
        '<span class="qty-display">' + qty + '</span>' +
        '<button class="qty-btn" data-act="inc">+</button>' +
        '</div>';
      container.querySelector('[data-act="dec"]').onclick = function () { setQty(sku, qty - 1); };
      container.querySelector('[data-act="inc"]').onclick = function () { setQty(sku, qty + 1); };
    }
  }

  // --- Cart ---
  function addToCart(sku) {
    cart[sku] = (cart[sku] || 0) + 1;
    saveCart();
    updateCartBar();
    renderCardButton(sku);
  }

  function setQty(sku, qty) {
    if (qty <= 0) { delete cart[sku]; } else { cart[sku] = qty; }
    saveCart();
    updateCartBar();
    renderCart();
    renderCardButton(sku);
  }

  function saveCart() {
    try { localStorage.setItem('coffee_cart', JSON.stringify(cart)); } catch (e) {}
  }

  function loadCart() {
    try { cart = JSON.parse(localStorage.getItem('coffee_cart') || '{}'); } catch (e) { cart = {}; }
  }

  function clearCart() {
    cart = {};
    try { localStorage.removeItem('coffee_cart'); } catch (e) {}
  }

  function cartTotal() {
    var total = 0;
    for (var sku in cart) {
      var item = menu.find(function (m) { return m.sku === sku; });
      if (item) total += item.price_cents * cart[sku];
    }
    return total;
  }

  function cartCount() {
    var n = 0;
    for (var sku in cart) n += cart[sku];
    return n;
  }

  function updateCartBar() {
    document.getElementById('cart-count').textContent = cartCount();
    document.getElementById('cart-total').textContent = fmt(cartTotal());
  }

  function renderCart() {
    var el = document.getElementById('cart-items');
    el.innerHTML = '';
    var hasItems = false;
    for (var sku in cart) {
      hasItems = true;
      var item = menu.find(function (m) { return m.sku === sku; });
      if (!item) continue;
      var row = document.createElement('div');
      row.className = 'cart-item';
      row.innerHTML =
        '<div class="cart-item-info">' +
        '<div class="name">' + esc(item.name) + '</div>' +
        '<div class="price">' + fmt(item.price_cents) + ' each</div>' +
        '</div>' +
        '<div class="qty-control">' +
        '<button class="qty-btn" data-act="dec">&minus;</button>' +
        '<span class="qty-display">' + cart[sku] + '</span>' +
        '<button class="qty-btn" data-act="inc">+</button>' +
        '</div>';
      (function (s) {
        row.querySelector('[data-act="dec"]').onclick = function () { setQty(s, cart[s] - 1); };
        row.querySelector('[data-act="inc"]').onclick = function () { setQty(s, cart[s] + 1); };
      })(sku);
      el.appendChild(row);
    }
    document.getElementById('cart-total-detail').textContent = fmt(cartTotal());

    var checkoutBtn = document.getElementById('checkout-btn');
    checkoutBtn.disabled = !hasItems;

    renderPickupOptions();
  }

  function renderPickupOptions() {
    var el = document.getElementById('pickup-options');
    el.innerHTML = '';
    var options = [
      { mins: 0, label: 'ASAP' },
      { mins: 15, label: '15 min' },
      { mins: 30, label: '30 min' },
      { mins: 45, label: '45 min' }
    ];
    options.forEach(function (opt) {
      var btn = document.createElement('button');
      btn.className = 'pickup-opt' + (opt.mins === selectedPickup ? ' active' : '');
      btn.textContent = opt.label;
      btn.onclick = function () {
        selectedPickup = opt.mins;
        renderPickupOptions();
      };
      el.appendChild(btn);
    });
  }

  // --- Place order ---
  function placeOrder() {
    var items = [];
    for (var sku in cart) {
      items.push({ sku: sku, quantity: cart[sku] });
    }
    var btn = document.getElementById('checkout-btn');
    btn.disabled = true;
    btn.textContent = 'Processing...';

    api('POST', '/api/orders', {
      items: items,
      pickup_minutes: selectedPickup,
      note: ''
    }).then(function (resp) {
      showPaymentScreen(resp);
    }).catch(function (e) {
      btn.disabled = false;
      btn.textContent = 'Place Order & Pay';
      showError(e.message);
    });
  }

  function showPaymentScreen(resp) {
    currentOrderID = resp.order_id;
    document.getElementById('payment-order-no').textContent = 'Order #' + resp.order_no;
    document.getElementById('payment-amount').textContent = fmt(resp.total_cents);
    document.getElementById('payment-pickup').textContent = 'Pickup: ' + (resp.pickup_time || 'ASAP');
    document.getElementById('qr-image').src = resp.qr_url;
    // Show test-pay button only in test mode.
    var testBtn = document.getElementById('test-pay-btn');
    if (resp.test_mode) {
      testBtn.classList.remove('hidden');
    } else {
      testBtn.classList.add('hidden');
    }
    showPayment();
    startPolling(resp.order_id);
  }

  // --- Polling for payment status ---
  function startPolling(orderID) {
    if (pollTimer) clearInterval(pollTimer);
    var attempts = 0;
    pollTimer = setInterval(function () {
      attempts++;
      if (attempts > 1800) { // 60 min at 2s interval
        clearInterval(pollTimer);
        return;
      }
      api('GET', '/api/orders/' + orderID).then(function (o) {
        if (o.status === 'paid' || o.status === 'ready') {
          clearInterval(pollTimer);
          showSuccessScreen(o);
        }
      }).catch(function () {});
    }, 2000);
  }

  function showSuccessScreen(o) {
    document.getElementById('success-order').textContent = 'Order #' + o.order_no;
    document.getElementById('success-detail').textContent = fmt(o.total_cents) + ' - Pickup: ' + (o.pickup_time || 'ASAP');
    clearCart();
    showSuccess();
  }

  // --- Util ---
  function esc(s) {
    var d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
  }

  // --- Test mode: simulate payment success ---
  function simulatePayment() {
    if (!currentOrderID) return;
    var btn = document.getElementById('test-pay-btn');
    btn.disabled = true;
    btn.textContent = 'Simulating...';
    api('POST', '/api/orders/test-pay/' + currentOrderID, {}).then(function () {
      // Polling will pick up status=paid and show the success screen.
    }).catch(function (e) {
      btn.disabled = false;
      btn.textContent = '[Test Mode] Simulate Payment Success';
      showError(e.message);
    });
  }

  // --- Init ---
  authenticate().then(function () {
    // 1. Check for a pending payment first.
    api('GET', '/api/orders/pending').then(function (resp) {
      if (resp) {
        // Resume the payment screen.
        showPaymentScreen(resp);
        return;
      }
      // 2. No pending payment — restore cart + load menu.
      loadCart();
      updateCartBar();
      loadMenu();
    }).catch(function () {
      // Fallback: just load the menu.
      loadCart();
      updateCartBar();
      loadMenu();
    });
  });

  // Expose for onclick handlers.
  window.showMenu = showMenu;
  window.showCart = showCart;
  window.placeOrder = placeOrder;
  window.simulatePayment = simulatePayment;
})();