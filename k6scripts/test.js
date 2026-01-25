import http from 'k6/http';
import { check, sleep } from 'k6';
import { randomItem } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

export const options = {
  scenarios: {
    million_shoppers: {
      executor: 'per-vu-iterations',
      vus: 1000, //000,
      iterations: 5,
      maxDuration: '4h',
    },
  },
};

const API_URL = __ENV.TARGET_DOMAIN;
const CHANNEL = 'default-channel';

// Запрос товаров (публичный)
const queryProducts = `
  query Products {
    products(first: 20, channel: "${CHANNEL}") {
      edges {
        node {
          id
          variants { id }
        }
      }
    }
  }
`;

const mutationCreateCheckout = `
  mutation CreateCheckout($variantId: ID!, $email: String!) {
    checkoutCreate(
      input: {
        channel: "${CHANNEL}",
        email: $email,
        lines: [{quantity: 1, variantId: $variantId}]
      }
    ) {
      checkout { id }
      errors { field message code }
    }
  }
`;

export default function () {
  const headers = { 'Content-Type': 'application/json' };
  
  // Генерируем случайный email для каждого теста, чтобы эмулировать разных гостей
  const email = `guest_${__VU}_${__ITER}@example.com`;

  // 1. КАТАЛОГ (Без токена)
  const resProducts = http.post(API_URL, JSON.stringify({ query: queryProducts }), { headers: headers });

  // Проверка: Если сервер лежит, не пытаемся парсить JSON, иначе скрипт упадет
  if (resProducts.status !== 200) {
    console.error(`❌ Ошибка каталога: ${resProducts.status} ${resProducts.status_text}`);
    sleep(1);
    return; // Пропускаем итерацию
  }
  
  let variantId = null;
  try {
    const edges = resProducts.json('data.products.edges');
    if (edges && edges.length > 0) {
      const randomProduct = randomItem(edges);
      if (randomProduct.node.variants.length > 0) {
        variantId = randomProduct.node.variants[0].id;
      }
    }
  } catch (e) {
    console.error('Ошибка парсинга товаров:', e);
  }

  // --- 2. ПОКУПКА ---
  if (variantId) {
    const resCheckout = http.post(API_URL, JSON.stringify({
      query: mutationCreateCheckout,
      variables: { variantId: variantId, email: email }
    }), { headers: headers });

    // То же самое: проверяем статус перед чтением JSON
    if (resCheckout.status !== 200) {
       console.error(`[x] Ошибка чекаута: ${resCheckout.status} ${resCheckout.status_text}`);
       // Форсируем провал проверки для статистики
       check(resCheckout, { 'Checkout Created': (r) => false });
    } else {
       const body = resCheckout.json();
       // Проверяем, есть ли тело ответа и нет ли ошибок GraphQL
       const checkoutId = body.data && body.data.checkoutCreate && body.data.checkoutCreate.checkout ? body.data.checkoutCreate.checkout.id : undefined;
       
       check(resCheckout, { 'Checkout Created': (r) => checkoutId !== undefined });
       
       if (!checkoutId && body.errors) {
         console.error('GraphQL Error:', JSON.stringify(body.errors));
       }
    }
  }

  sleep(1);
}