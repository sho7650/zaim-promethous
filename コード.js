// ========================================
// 設定値取得
// ========================================
function getConfig() {
  const scriptProps = PropertiesService.getScriptProperties();
  const userProps = PropertiesService.getUserProperties();
  
  let accessToken = null;
  let accessTokenSecret = null;
  
  const oauthData = userProps.getProperty('oauth1.zaim');
  if (oauthData) {
    try {
      const parsed = JSON.parse(oauthData);
      accessToken = parsed.public;
      accessTokenSecret = parsed.secret;
    } catch (e) {
      Logger.log('oauth1.zaim のパースエラー: ' + e.toString());
    }
  }
  
  return {
    consumerKey: scriptProps.getProperty('CONSUMER_KEY'),
    consumerSecret: scriptProps.getProperty('CONSUMER_SECRET'),
    accessToken: accessToken,
    accessTokenSecret: accessTokenSecret,
  };
}

// ========================================
// OAuth サービス設定
// ========================================
function getZaimService() {
  const config = getConfig();
  
  return OAuth1.createService('zaim')
    .setAccessTokenUrl('https://api.zaim.net/v2/auth/access')
    .setRequestTokenUrl('https://api.zaim.net/v2/auth/request')
    .setAuthorizationUrl('https://auth.zaim.net/users/auth')
    .setConsumerKey(config.consumerKey)
    .setConsumerSecret(config.consumerSecret)
    .setCallbackFunction('authCallback')
    .setPropertyStore(PropertiesService.getUserProperties());
}

// ========================================
// Zaim API からデータ取得
// ========================================
function getZaimData() {
  const service = getZaimService();
  
  if (!service.hasAccess()) {
    throw new Error('Zaim 認証が必要です');
  }
  
  const today = new Date();
  
  // 今月の初日を取得
  const firstDayOfMonth = new Date(today.getFullYear(), today.getMonth(), 1);
  
  const startDate = Utilities.formatDate(firstDayOfMonth, 'Asia/Tokyo', 'yyyy-MM-dd');
  const endDate = Utilities.formatDate(today, 'Asia/Tokyo', 'yyyy-MM-dd');
  
  Logger.log(`データ取得期間: ${startDate} ～ ${endDate}`);
  
  const url = `https://api.zaim.net/v2/home/money?mapping=1&start_date=${startDate}&end_date=${endDate}&limit=100`;
  
  const response = service.fetch(url, {
    method: 'GET',
    muteHttpExceptions: true
  });
  
  const code = response.getResponseCode();
  if (code !== 200) {
    throw new Error(`Zaim API returned ${code}`);
  }
  
  return JSON.parse(response.getContentText());
}

// ========================================
// 1時間単位で集計
// ========================================
function aggregateByHour(moneyData) {
  const hourlyData = {};
  
  moneyData.money.forEach(item => {
    if (item.mode !== 'payment') return;
    
    const datetime = new Date(item.created);
    const hour = Utilities.formatDate(datetime, 'Asia/Tokyo', 'yyyy-MM-dd HH:00:00');
    
    if (!hourlyData[hour]) {
      hourlyData[hour] = {
        total: 0,
        count: 0,
        categories: {}
      };
    }
    
    hourlyData[hour].total += item.amount;
    hourlyData[hour].count += 1;
    
    const categoryId = item.category_id;
    if (!hourlyData[hour].categories[categoryId]) {
      hourlyData[hour].categories[categoryId] = 0;
    }
    hourlyData[hour].categories[categoryId] += item.amount;
  });
  
  return hourlyData;
}

// ========================================
// 日別に集計
// ========================================
function aggregateByDay(moneyData) {
  const dailyData = {};
  
  moneyData.money.forEach(item => {
    if (item.mode !== 'payment') return;
    
    const date = item.date; // YYYY-MM-DD 形式
    
    if (!dailyData[date]) {
      dailyData[date] = {
        total: 0,
        count: 0,
        categories: {}
      };
    }
    
    dailyData[date].total += item.amount;
    dailyData[date].count += 1;
    
    const categoryId = item.category_id;
    if (!dailyData[date].categories[categoryId]) {
      dailyData[date].categories[categoryId] = 0;
    }
    dailyData[date].categories[categoryId] += item.amount;
  });
  
  return dailyData;
}

// ========================================
// Prometheus メトリクス形式を生成
// ========================================
function generatePrometheusMetrics(hourlyData, dailyData) {
  let metrics = '';
  
  // ========================================
  // 時間別メトリクス
  // ========================================
  metrics += '# HELP zaim_hourly_expense_total Total expenses per hour in JPY\n';
  metrics += '# TYPE zaim_hourly_expense_total gauge\n';
  Object.keys(hourlyData).forEach(hour => {
    const data = hourlyData[hour];
    metrics += `zaim_hourly_expense_total{hour="${hour}"} ${data.total}\n`;
  });
  metrics += '\n';
  
  metrics += '# HELP zaim_hourly_transaction_count Number of transactions per hour\n';
  metrics += '# TYPE zaim_hourly_transaction_count gauge\n';
  Object.keys(hourlyData).forEach(hour => {
    const data = hourlyData[hour];
    const timestamp = new Date(hour).getTime();
    metrics += `zaim_hourly_transaction_count{hour="${hour}"} ${data.count}\n`;
  });
  metrics += '\n';
  
  metrics += '# HELP zaim_hourly_expense_by_category Expenses per hour by category in JPY\n';
  metrics += '# TYPE zaim_hourly_expense_by_category gauge\n';
  Object.keys(hourlyData).forEach(hour => {
    const data = hourlyData[hour];
    const timestamp = new Date(hour).getTime();
    Object.keys(data.categories).forEach(categoryId => {
      metrics += `zaim_hourly_expense_by_category{hour="${hour}",category_id="${categoryId}"} ${data.categories[categoryId]}\n`;
    });
  });
  metrics += '\n';
  
  // ========================================
  // 日別メトリクス
  // ========================================
  metrics += '# HELP zaim_daily_expense Daily expenses in JPY\n';
  metrics += '# TYPE zaim_daily_expense gauge\n';
  Object.keys(dailyData).forEach(date => {
    const data = dailyData[date];
    const timestamp = new Date(date + ' 00:00:00').getTime();
    metrics += `zaim_daily_expense{date="${date}"} ${data.total}\n`;
  });
  metrics += '\n';
  
  metrics += '# HELP zaim_daily_transaction_count_by_date Daily transaction count\n';
  metrics += '# TYPE zaim_daily_transaction_count_by_date gauge\n';
  Object.keys(dailyData).forEach(date => {
    const data = dailyData[date];
    const timestamp = new Date(date + ' 00:00:00').getTime();
    metrics += `zaim_daily_transaction_count_by_date{date="${date}"} ${data.count}\n`;
  });
  metrics += '\n';
  
  metrics += '# HELP zaim_daily_expense_by_category Daily expenses by category in JPY\n';
  metrics += '# TYPE zaim_daily_expense_by_category gauge\n';
  Object.keys(dailyData).forEach(date => {
    const data = dailyData[date];
    const timestamp = new Date(date + ' 00:00:00').getTime();
    Object.keys(data.categories).forEach(categoryId => {
      metrics += `zaim_daily_expense_by_category{date="${date}",category_id="${categoryId}"} ${data.categories[categoryId]}\n`;
    });
  });
  metrics += '\n';
  
  // ========================================
  // 本日の合計
  // ========================================
  let todayTotal = 0;
  let todayCount = 0;
  Object.keys(hourlyData).forEach(hour => {
    todayTotal += hourlyData[hour].total;
    todayCount += hourlyData[hour].count;
  });
  
  metrics += '# HELP zaim_today_expense_total Total expenses today in JPY\n';
  metrics += '# TYPE zaim_today_expense_total gauge\n';
  metrics += `zaim_today_expense_total ${todayTotal}\n`;
  metrics += '\n';
  
  metrics += '# HELP zaim_today_transaction_count Total transaction count today\n';
  metrics += '# TYPE zaim_today_transaction_count gauge\n';
  metrics += `zaim_today_transaction_count ${todayCount}\n`;
  metrics += '\n';
  
  // ========================================
  // 今月の合計
  // ========================================
  let monthlyTotal = 0;
  let monthlyCount = 0;
  Object.keys(dailyData).forEach(date => {
    monthlyTotal += dailyData[date].total;
    monthlyCount += dailyData[date].count;
  });
  
  metrics += '# HELP zaim_monthly_expense_total Total expenses this month in JPY\n';
  metrics += '# TYPE zaim_monthly_expense_total gauge\n';
  metrics += `zaim_monthly_expense_total ${monthlyTotal}\n`;
  metrics += '\n';
  
  metrics += '# HELP zaim_monthly_transaction_count Total transaction count this month\n';
  metrics += '# TYPE zaim_monthly_transaction_count gauge\n';
  metrics += `zaim_monthly_transaction_count ${monthlyCount}\n`;
  metrics += '\n';
  
  // ========================================
  // カテゴリ別の月間合計
  // ========================================
  const monthlyCategoryTotals = {};
  Object.keys(dailyData).forEach(date => {
    const data = dailyData[date];
    Object.keys(data.categories).forEach(categoryId => {
      if (!monthlyCategoryTotals[categoryId]) {
        monthlyCategoryTotals[categoryId] = 0;
      }
      monthlyCategoryTotals[categoryId] += data.categories[categoryId];
    });
  });
  
  metrics += '# HELP zaim_monthly_expense_by_category Monthly expenses by category in JPY\n';
  metrics += '# TYPE zaim_monthly_expense_by_category gauge\n';
  Object.keys(monthlyCategoryTotals).forEach(categoryId => {
    metrics += `zaim_monthly_expense_by_category{category_id="${categoryId}"} ${monthlyCategoryTotals[categoryId]}\n`;
  });
  metrics += '\n';
  
  // ========================================
  // 最終更新時刻
  // ========================================
  metrics += '# HELP zaim_last_scrape_timestamp_seconds Last scrape timestamp\n';
  metrics += '# TYPE zaim_last_scrape_timestamp_seconds gauge\n';
  metrics += `zaim_last_scrape_timestamp_seconds ${Date.now() / 1000}\n`;
  
  return metrics;
}

// ========================================
// Prometheus Exporter として動作
// ========================================
/*--
function doGet(e) {
  Logger.log('=== doGet 呼び出し ===');
  Logger.log('パラメータ: ' + JSON.stringify(e.parameter));
  
  const authToken = e.parameter.token;
  const expectedToken = PropertiesService.getScriptProperties().getProperty('API_TOKEN');
  
  if (expectedToken && authToken !== expectedToken) {
    Logger.log('認証失敗');
    return ContentService.createTextOutput('Unauthorized\n')
      .setMimeType(ContentService.MimeType.TEXT);
  }
  
  try {
    Logger.log('Zaim API にアクセス中...');
    
    const cache = CacheService.getScriptCache();
    const cacheKey = 'zaim_metrics';
    let metrics = cache.get(cacheKey);
    
    if (!metrics) {
      Logger.log('キャッシュミス: データ取得開始');
      
      const zaimData = getZaimData();
      
      if (!zaimData || !zaimData.money || zaimData.money.length === 0) {
        Logger.log('データなし');
        metrics = '# No data available\n';
      } else {
        Logger.log('データ取得成功: ' + zaimData.money.length + ' 件');
        
        // 1時間単位で集計
        const hourlyData = aggregateByHour(zaimData);
        Logger.log('時間別集計完了: ' + Object.keys(hourlyData).length + ' 時間帯');
        
        // 日別で集計
        const dailyData = aggregateByDay(zaimData);
        Logger.log('日別集計完了: ' + Object.keys(dailyData).length + ' 日');
        
        // メトリクス生成
        metrics = generatePrometheusMetrics(hourlyData, dailyData);
      }
      
      // キャッシュに保存（5分間）
      cache.put(cacheKey, metrics, 300);
      Logger.log('キャッシュに保存');
    } else {
      Logger.log('キャッシュヒット');
    }
    
    Logger.log('メトリクス返却: ' + metrics.length + ' バイト');
    
    return ContentService.createTextOutput(metrics)
      .setMimeType(ContentService.MimeType.TEXT);
      
  } catch (error) {
    Logger.log('エラー: ' + error.toString());
    Logger.log(error.stack);
    
    return ContentService.createTextOutput('# Error: ' + error.toString() + '\n')
      .setMimeType(ContentService.MimeType.TEXT);
  }
}
*/

// ========================================
// メインエントリーポイント
// ========================================
function doGet(e) {
  Logger.log('=== doGet 呼び出し ===');
  Logger.log('パラメータ: ' + JSON.stringify(e.parameter));
  
  // OAuth コールバック処理（最優先）
  if (e.parameter.oauth_token && e.parameter.oauth_verifier) {
    return handleOAuthCallback(e);
  }
  
  // モード判定
  const mode = e.parameter.mode || 'metrics';
  
  // モードに応じて処理を分岐
  switch (mode) {
    case 'auth':
      return showAuthPage(e);
    
    case 'reset':
      return resetAuth(e);
    
    case 'metrics':
    default:
      return serveMetrics(e);
  }
}

// ========================================
// 認証ページを表示
// ========================================
function showAuthPage(e) {
  const service = getZaimService();
  
  // 既に認証済み
  if (service.hasAccess()) {
    const userProps = PropertiesService.getUserProperties();
    const oauthData = userProps.getProperty('oauth1.zaim');
    
    let token = null;
    let tokenSecret = null;
    
    if (oauthData) {
      try {
        const parsed = JSON.parse(oauthData);
        token = parsed.public;
        tokenSecret = parsed.secret;
      } catch (e) {
        Logger.log('パースエラー: ' + e.toString());
      }
    }
    
    const apiToken = PropertiesService.getScriptProperties().getProperty('API_TOKEN');
    const metricsUrl = ScriptApp.getService().getUrl() + '?mode=metrics&token=' + apiToken;
    
    return HtmlService.createHtmlOutput(`
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="UTF-8">
        <title>Zaim 認証状態</title>
        <style>
          body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
          h2 { color: #4CAF50; }
          pre { background: #f5f5f5; padding: 15px; border-radius: 5px; overflow-x: auto; font-size: 12px; word-wrap: break-word; }
          a { color: #2196F3; text-decoration: none; }
          a:hover { text-decoration: underline; }
          .button { display: inline-block; padding: 10px 20px; background-color: #2196F3; color: white; border-radius: 5px; margin: 10px 5px; }
          .button:hover { background-color: #1976D2; text-decoration: none; }
          .danger { background-color: #f44336; }
          .danger:hover { background-color: #d32f2f; }
        </style>
      </head>
      <body>
        <h2>✅ 認証済みです</h2>
        <p>Zaim との連携は正常に動作しています。</p>
        
        <hr>
        
        <h3>トークン情報</h3>
        <p><strong>ACCESS_TOKEN:</strong></p>
        <pre>${token || '取得できませんでした'}</pre>
        
        <p><strong>ACCESS_TOKEN_SECRET:</strong></p>
        <pre>${tokenSecret || '取得できませんでした'}</pre>
        
        <hr>
        
        <h3>アクション</h3>
        <a href="?mode=reset" class="button danger">認証をリセット</a>
        <a href="${metricsUrl}" class="button" target="_blank">メトリクスを表示</a>
        
        <hr>
        
        <h3>Prometheus 設定</h3>
        <p>以下の URL でメトリクスを取得できます：</p>
        <pre>${metricsUrl}</pre>
      </body>
      </html>
    `);
  }
  
  // 認証開始
  try {
    const authorizationUrl = service.authorize();
    const webAppUrl = ScriptApp.getService().getUrl();
    
    return HtmlService.createHtmlOutput(`
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="UTF-8">
        <title>Zaim 認証</title>
        <style>
          body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
          h2 { color: #FF9800; }
          .auth-button { display: inline-block; padding: 15px 30px; background-color: #4CAF50; color: white; text-decoration: none; border-radius: 5px; font-weight: bold; font-size: 16px; }
          .auth-button:hover { background-color: #45a049; }
          ul { line-height: 1.8; }
          code { background: #f5f5f5; padding: 2px 6px; border-radius: 3px; }
        </style>
      </head>
      <body>
        <h2>Zaim 認証</h2>
        <p>以下のリンクをクリックして、Zaim で認証してください。</p>
        
        <p style="text-align: center; margin: 30px 0;">
          <a href="${authorizationUrl}" target="_blank" class="auth-button">Zaim で認証する</a>
        </p>
        
        <hr>
        
        <h3>事前確認</h3>
        <ul>
          <li>Zaim Developer (<a href="https://dev.zaim.net/" target="_blank">https://dev.zaim.net/</a>) でアプリを登録済みですか？</li>
          <li>サービスのURL に <code>${webAppUrl}</code> を設定しましたか？</li>
          <li>「家計簿に書き込まれた記録を読み取る」にチェックを入れましたか？</li>
          <li>「家計簿に恒久的にアクセスする」にチェックを入れましたか？</li>
        </ul>
        
        <hr>
        
        <h3>トラブルシューティング</h3>
        <p>認証がうまくいかない場合：</p>
        <ul>
          <li>CONSUMER_KEY と CONSUMER_SECRET がスクリプトプロパティに正しく設定されているか確認</li>
          <li>Zaim Developer のサービス URL が完全に一致しているか確認（末尾のスラッシュも含めて）</li>
          <li>ブラウザのキャッシュをクリアして再試行</li>
        </ul>
      </body>
      </html>
    `);
  } catch (error) {
    return HtmlService.createHtmlOutput(`
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="UTF-8">
        <title>エラー</title>
        <style>
          body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
          h2 { color: #f44336; }
          pre { background: #ffebee; padding: 15px; border-radius: 5px; color: #c62828; overflow-x: auto; }
          ul { line-height: 1.8; }
        </style>
      </head>
      <body>
        <h2>❌ エラーが発生しました</h2>
        <pre>${error.toString()}</pre>
        
        <hr>
        
        <h3>確認事項</h3>
        <ul>
          <li>スクリプトプロパティに CONSUMER_KEY が設定されていますか？</li>
          <li>スクリプトプロパティに CONSUMER_SECRET が設定されていますか？</li>
          <li>OAuth1 ライブラリが追加されていますか？</li>
        </ul>
        
        <p><a href="?mode=auth">再読み込み</a></p>
      </body>
      </html>
    `);
  }
}

// ========================================
// 認証をリセット
// ========================================
function resetAuth(e) {
  try {
    const service = getZaimService();
    service.reset();
    
    Logger.log('✅ 認証をリセットしました');
    
    return HtmlService.createHtmlOutput(`
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="UTF-8">
        <title>認証リセット完了</title>
        <style>
          body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; text-align: center; }
          h2 { color: #4CAF50; }
          .button { display: inline-block; padding: 15px 30px; background-color: #4CAF50; color: white; text-decoration: none; border-radius: 5px; font-weight: bold; margin: 20px; }
          .button:hover { background-color: #45a049; }
        </style>
      </head>
      <body>
        <h2>✅ 認証をリセットしました</h2>
        <p>Zaim の認証情報を削除しました。</p>
        <p>再度認証を行ってください。</p>
        
        <a href="?mode=auth" class="button">再度認証する</a>
      </body>
      </html>
    `);
  } catch (error) {
    Logger.log('❌ リセットエラー: ' + error.toString());
    
    return HtmlService.createHtmlOutput(`
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="UTF-8">
        <title>エラー</title>
        <style>
          body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
          h2 { color: #f44336; }
          pre { background: #ffebee; padding: 15px; border-radius: 5px; color: #c62828; }
        </style>
      </head>
      <body>
        <h2>❌ エラーが発生しました</h2>
        <pre>${error.toString()}</pre>
        <p><a href="?mode=auth">認証ページに戻る</a></p>
      </body>
      </html>
    `);
  }
}

// ========================================
// OAuth コールバック処理
// ========================================
function handleOAuthCallback(e) {
  Logger.log('=== OAuth コールバック処理 ===');
  
  try {
    const service = getZaimService();
    const isAuthorized = service.handleCallback(e);
    
    Logger.log('isAuthorized: ' + isAuthorized);
    
    if (isAuthorized) {
      // 少し待ってからトークンを取得
      Utilities.sleep(500);
      
      const userProps = PropertiesService.getUserProperties();
      const oauthData = userProps.getProperty('oauth1.zaim');
      
      let token = null;
      let tokenSecret = null;
      
      if (oauthData) {
        try {
          const parsed = JSON.parse(oauthData);
          token = parsed.public;
          tokenSecret = parsed.secret;
          
          Logger.log('✅ トークン取得成功');
        } catch (e) {
          Logger.log('❌ パースエラー: ' + e.toString());
        }
      }
      
      return HtmlService.createHtmlOutput(`
        <!DOCTYPE html>
        <html>
        <head>
          <meta charset="UTF-8">
          <title>認証成功</title>
          <style>
            body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
            h2 { color: #4CAF50; }
            pre { background: #f5f5f5; padding: 15px; border-radius: 5px; overflow-x: auto; font-size: 12px; word-wrap: break-word; }
            .button { display: inline-block; padding: 10px 20px; background-color: #4CAF50; color: white; text-decoration: none; border-radius: 5px; margin: 10px 5px; }
            .button:hover { background-color: #45a049; text-decoration: none; }
          </style>
        </head>
        <body>
          <h2>✅ 認証成功！</h2>
          <p>トークンはユーザープロパティに自動保存されました。</p>
          
          <hr>
          
          <h3>保存されたトークン</h3>
          <p><strong>ACCESS_TOKEN:</strong></p>
          <pre>${token || '取得できませんでした'}</pre>
          
          <p><strong>ACCESS_TOKEN_SECRET:</strong></p>
          <pre>${tokenSecret || '取得できませんでした'}</pre>
          
          <hr>
          
          <h3>次のステップ</h3>
          <ol>
            <li>このページを閉じる</li>
            <li>Prometheus が自動的にメトリクスを取得します</li>
            <li>Grafana でダッシュボードを確認してください</li>
          </ol>
          
          <a href="?mode=auth" class="button">認証状態を確認</a>
        </body>
        </html>
      `);
    } else {
      Logger.log('❌ 認証失敗');
      
      return HtmlService.createHtmlOutput(`
        <!DOCTYPE html>
        <html>
        <head>
          <meta charset="UTF-8">
          <title>認証失敗</title>
          <style>
            body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
            h2 { color: #f44336; }
            .button { display: inline-block; padding: 10px 20px; background-color: #2196F3; color: white; text-decoration: none; border-radius: 5px; }
            .button:hover { background-color: #1976D2; }
          </style>
        </head>
        <body>
          <h2>❌ 認証失敗</h2>
          <p>もう一度やり直してください。</p>
          
          <h3>考えられる原因</h3>
          <ul>
            <li>Zaim Developer のサービスURL が正しくない</li>
            <li>CONSUMER_KEY または CONSUMER_SECRET が間違っている</li>
            <li>認証を途中でキャンセルした</li>
          </ul>
          
          <a href="?mode=auth" class="button">最初から</a>
        </body>
        </html>
      `);
    }
  } catch (error) {
    Logger.log('❌ コールバックエラー: ' + error.toString());
    
    return HtmlService.createHtmlOutput(`
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="UTF-8">
        <title>エラー</title>
        <style>
          body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
          h2 { color: #f44336; }
          pre { background: #ffebee; padding: 15px; border-radius: 5px; color: #c62828; overflow-x: auto; }
        </style>
      </head>
      <body>
        <h2>❌ エラーが発生しました</h2>
        <pre>${error.toString()}</pre>
        <p><a href="?mode=auth">最初から</a></p>
      </body>
      </html>
    `);
  }
}

// ========================================
// メトリクスを提供
// ========================================
function serveMetrics(e) {
  const authToken = e.parameter.token;
  const expectedToken = PropertiesService.getScriptProperties().getProperty('API_TOKEN');
  
  // トークン認証
  if (expectedToken && authToken !== expectedToken) {
    Logger.log('❌ 認証失敗: トークンが一致しません');
    return ContentService.createTextOutput('Unauthorized\n')
      .setMimeType(ContentService.MimeType.TEXT);
  }
  
  try {
    Logger.log('📊 メトリクス取得開始');
    
    // キャッシュをチェック
    const cache = CacheService.getScriptCache();
    const cacheKey = 'zaim_metrics';
    let metrics = cache.get(cacheKey);
    
    if (!metrics) {
      Logger.log('キャッシュミス: Zaim API にアクセス');
      
      const zaimData = getZaimData();
      
      if (!zaimData || !zaimData.money || zaimData.money.length === 0) {
        Logger.log('⚠️ データなし');
        metrics = '# No data available\n';
      } else {
        Logger.log('✅ データ取得成功: ' + zaimData.money.length + ' 件');
        
        const hourlyData = aggregateByHour(zaimData);
        Logger.log('時間別集計: ' + Object.keys(hourlyData).length + ' 時間帯');
        
        const dailyData = aggregateByDay(zaimData);
        Logger.log('日別集計: ' + Object.keys(dailyData).length + ' 日');
        
        metrics = generatePrometheusMetrics(hourlyData, dailyData);
      }
      
      // キャッシュに保存（5分間）
      cache.put(cacheKey, metrics, 300);
      Logger.log('✅ キャッシュに保存');
    } else {
      Logger.log('✅ キャッシュヒット');
    }
    
    Logger.log('📤 メトリクス返却: ' + metrics.length + ' バイト');
    
    return ContentService.createTextOutput(metrics)
      .setMimeType(ContentService.MimeType.TEXT);
      
  } catch (error) {
    Logger.log('❌ エラー: ' + error.toString());
    Logger.log(error.stack);
    
    // エラーメッセージをメトリクス形式で返す
    const errorMetrics = '# Error occurred\n' +
                        '# ' + error.toString() + '\n' +
                        'zaim_error{type="data_fetch"} 1\n';
    
    return ContentService.createTextOutput(errorMetrics)
      .setMimeType(ContentService.MimeType.TEXT);
  }
}

// ========================================
// OAuth コールバック関数（OAuth1 ライブラリから呼ばれる）
// ========================================
function authCallback(request) {
  const service = getZaimService();
  return service.handleCallback(request);
}

// ========================================
// デバッグ・診断用関数
// ========================================
function debugGetConfig() {
  Logger.log('=== getConfig() デバッグ ===');
  
  const scriptProps = PropertiesService.getScriptProperties();
  Logger.log('【スクリプトプロパティ】');
  Logger.log('CONSUMER_KEY: ' + scriptProps.getProperty('CONSUMER_KEY'));
  Logger.log('CONSUMER_SECRET: ' + (scriptProps.getProperty('CONSUMER_SECRET') ? '設定済み' : '未設定'));
  Logger.log('API_TOKEN: ' + (scriptProps.getProperty('API_TOKEN') ? '設定済み' : '未設定'));
  Logger.log('');
  
  const userProps = PropertiesService.getUserProperties();
  Logger.log('【ユーザープロパティ】');
  const oauthData = userProps.getProperty('oauth1.zaim');
  Logger.log('oauth1.zaim: ' + (oauthData ? oauthData.substring(0, 100) + '...' : '未設定'));
  
  if (oauthData) {
    const parsed = JSON.parse(oauthData);
    Logger.log('  public: ' + parsed.public);
    Logger.log('  secret: ' + parsed.secret);
    Logger.log('  type: ' + parsed.type);
  }
  Logger.log('');
  
  Logger.log('【getConfig() の返り値】');
  const config = getConfig();
  Logger.log('consumerKey: ' + config.consumerKey);
  Logger.log('consumerSecret: ' + (config.consumerSecret ? '設定済み' : '未設定'));
  Logger.log('accessToken: ' + config.accessToken);
  Logger.log('accessTokenSecret: ' + config.accessTokenSecret);
  Logger.log('');
  
  Logger.log('【比較】');
  if (config.consumerKey === config.accessToken) {
    Logger.log('❌ Consumer Key と Access Token が同じです！');
  } else {
    Logger.log('✅ Consumer Key と Access Token は異なります');
  }
}

function testZaimAPI() {
  Logger.log('=== Zaim API テスト ===');
  
  try {
    const zaimData = getZaimData();
    Logger.log('✅ データ取得成功: ' + zaimData.money.length + ' 件');
    
    const hourlyData = aggregateByHour(zaimData);
    Logger.log('✅ 時間別集計: ' + Object.keys(hourlyData).length + ' 時間帯');
    
    const dailyData = aggregateByDay(zaimData);
    Logger.log('✅ 日別集計: ' + Object.keys(dailyData).length + ' 日');
    
    const metrics = generatePrometheusMetrics(hourlyData, dailyData);
    Logger.log('✅ メトリクス生成: ' + metrics.length + ' バイト');
    
    Logger.log('');
    Logger.log('メトリクスの一部:');
    Logger.log(metrics.substring(0, 500));
  } catch (error) {
    Logger.log('❌ エラー: ' + error.toString());
  }
}
