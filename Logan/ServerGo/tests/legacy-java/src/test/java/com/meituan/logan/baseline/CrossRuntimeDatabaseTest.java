package com.meituan.logan.baseline;

import com.alibaba.fastjson.JSON;
import com.alibaba.fastjson.JSONObject;
import com.meituan.logan.web.dto.*;
import com.meituan.logan.web.mapper.*;
import com.meituan.logan.web.handler.*;
import com.meituan.logan.web.model.request.LoganTaskRequest;
import com.meituan.logan.web.parser.WebLogParser;
import com.meituan.logan.web.service.impl.*;
import org.apache.ibatis.builder.xml.XMLMapperBuilder;
import org.apache.ibatis.datasource.unpooled.UnpooledDataSource;
import org.apache.ibatis.mapping.Environment;
import org.apache.ibatis.session.*;
import org.apache.ibatis.transaction.jdbc.JdbcTransactionFactory;
import org.junit.Assume;
import org.junit.Test;
import org.springframework.test.util.ReflectionTestUtils;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.sql.*;
import java.util.*;
import java.util.zip.GZIPOutputStream;
import static org.junit.Assert.*;

/** Opt-in disposable MySQL rehearsal. Every mapper and SQL statement comes from original Java. */
public class CrossRuntimeDatabaseTest {
    private static final long TIME = 1792022400000L;
    private static final String PAYLOAD = "crossruntime 中文 + percent%";
    private static String required(String key) {
        String value = System.getenv(key);
        if (value == null || value.isEmpty()) throw new IllegalArgumentException("Missing " + key);
        return value;
    }
    private static Path directory() throws IOException {
        Path dir = Paths.get(required("CROSSRUNTIME_DIR"));
        Files.createDirectories(dir);
        return dir;
    }
    private static void write(Path path, Object object) throws IOException {
        Files.write(path, JSON.toJSONString(object, true).getBytes(StandardCharsets.UTF_8));
    }
    private static JSONObject read(Path path) throws IOException {
        return JSON.parseObject(new String(Files.readAllBytes(path), StandardCharsets.UTF_8));
    }
    @Test public void originalJavaDatabaseRoundTrip() throws Exception {
        Assume.assumeTrue("opt-in integration only", System.getenv("CROSSRUNTIME_PHASE") != null);
        assertEquals("Disposable database acknowledgement required", "YES", required("CROSSRUNTIME_DISPOSABLE"));
        String jdbc = required("CROSSRUNTIME_JDBC_URL");
        assertTrue("Only random rehearsal databases allowed", jdbc.matches("jdbc:mysql://[^/]+/logan_crossruntime_[a-z0-9]+(?:\\?.*)?"));
        UnpooledDataSource dataSource = new UnpooledDataSource("com.mysql.cj.jdbc.Driver", jdbc,
                required("CROSSRUNTIME_DB_USER"), required("CROSSRUNTIME_DB_PASSWORD"));
        Configuration cfg = new Configuration(new Environment("disposable-crossruntime", new JdbcTransactionFactory(), dataSource));
        String mapperDir = System.getenv("CROSSRUNTIME_MAPPER_DIR");
        Path resources = Paths.get(mapperDir == null ? "../../../Server/src/main/resources/sqlmap" : mapperDir);
        for (String name : Arrays.asList("LoganTaskMapper", "LoganLogDetailMapper", "WebLogTaskMapper", "WebLogDetailMapper")) {
            Path source = resources.resolve(name + ".xml");
            try (InputStream in = Files.newInputStream(source)) {
                new XMLMapperBuilder(in, cfg, source.toString(), cfg.getSqlFragments()).parse();
            }
        }
        try (SqlSession session = new SqlSessionFactoryBuilder().build(cfg).openSession(true)) {
            if ("seed".equals(required("CROSSRUNTIME_PHASE"))) seed(session);
            else if ("verify".equals(required("CROSSRUNTIME_PHASE"))) verify(session);
            else fail("Unknown phase");
        }
    }
    private static long nativeTask(SqlSession s, String device, String key) {
        LoganTaskDTO task = new LoganTaskDTO();
        task.setDeviceId(device); task.setPlatform(1); task.setAmount("17"); task.setAppId("crossruntime");
        task.setUnionId("union-中文"); task.setBuildVersion("1"); task.setAppVersion("1.0");
        task.setLogFileName(key); task.setLogDate(TIME); task.setAddTime(TIME);
        s.getMapper(LoganTaskMapper.class).insert(task);
        LoganTaskRequest req = new LoganTaskRequest(device, null, null, null);
        List<LoganTaskDTO> rows = s.getMapper(LoganTaskMapper.class).search(req);
        assertEquals(1, rows.size()); return rows.get(0).getId();
    }
    private static long webTask(SqlSession s, String device, int status, String content) {
        WebLogTaskDTO task = new WebLogTaskDTO();
        task.setDeviceId(device); task.setPageNum(1); task.setWebSource("crossruntime");
        task.setEnvironment("disposable-only"); task.setLogDate(TIME); task.setAddTime(TIME);
        task.setContent(content); task.setStatus(status); task.setCustomReportInfo("{\"source\":\"java\"}");
        s.getMapper(WebLogTaskMapper.class).insert(task);
        return s.getMapper(WebLogTaskMapper.class).exist(TIME, device, 1).getTaskId();
    }
    private static void seed(SqlSession s) throws Exception {
        // Do not seed an occupied database or accidentally make a second fixture set.
        try (Statement st = s.getConnection().createStatement()) {
            for (String table : Arrays.asList("logan_task", "logan_log_detail", "web_task", "web_detail")) {
                try (ResultSet rows = st.executeQuery("SELECT COUNT(*) FROM " + table)) {
                    rows.next(); assertEquals("Database must start empty: " + table, 0L, rows.getLong(1));
                }
            }
        }
        JSONObject manifest = new JSONObject(true);
        long pending = nativeTask(s, "java-native-pending", "java-pending.gz");
        long completed = nativeTask(s, "java-native-completed", "java-completed.gz");
        s.getMapper(LoganTaskMapper.class).updateStatus(completed, 1);
        LoganLogDetailDTO detail = new LoganLogDetailDTO(); detail.setTaskId(completed);
        detail.setLogType(1); detail.setContent(PAYLOAD); detail.setLogTime(TIME + 60000);
        s.getMapper(LoganLogDetailMapper.class).batchInsert(Collections.singletonList(detail));
        long detailId = s.getMapper(LoganLogDetailMapper.class).queryByTaskIdTypeKeyword(completed, Collections.singletonList(1), PAYLOAD).get(0).getId();
        String webLog = "{\"t\":\"1\",\"c\":\"crossruntime%20%E4%B8%AD%E6%96%87%20%2B%20percent%25\",\"d\":\"" + (TIME+60000) + "\"}";
        String webContent = JSON.toJSONString(Collections.singletonList(webLog));
        long webPending = webTask(s, "java-web-pending", 0, webContent);
        long webCompleted = webTask(s, "java-web-completed", 1, webContent);
        List<WebLogDetailDTO> webDetails = WebLogParser.parseWebLogDetail(webContent, webCompleted);
        assertEquals(PAYLOAD, webDetails.get(0).getContent());
        s.getMapper(WebLogDetailMapper.class).batchInsert(webDetails);
        long webDetailId = s.getMapper(WebLogDetailMapper.class).match(webCompleted,0,1440,Collections.singletonList(1)).get(0).getId();
        Path dir = directory();
        try (GZIPOutputStream out = new GZIPOutputStream(Files.newOutputStream(dir.resolve("java-pending.gz")))) {
            out.write(("{\"c\":" + JSON.toJSONString(PAYLOAD) + ",\"f\":1,\"l\":" + (TIME+60000) + ",\"n\":\"main\",\"i\":1,\"m\":true}\n").getBytes(StandardCharsets.UTF_8));
        }
        manifest.put("nativePending", pending); manifest.put("nativeCompleted", completed);
        manifest.put("nativeCompletedDetail", detailId); manifest.put("webPending", webPending);
        manifest.put("webCompleted", webCompleted); manifest.put("webCompletedDetail", webDetailId);
        manifest.put("payload", PAYLOAD); manifest.put("time", TIME); manifest.put("nativeObjectKey", "java-pending.gz");
        manifest.put("evidence", "Untouched original Java DTOs, MyBatis mappers/XML and WebLogParser against disposable MySQL; gzip is a local fixture, not real OSS");
        write(dir.resolve("java-seed.json"), manifest);
        verifySeed(s, manifest, false);
        System.out.println("JAVA_SEED_VERIFIED");
    }
    private static void verifySeed(SqlSession s, JSONObject seed, boolean analyzed) {
        LoganTaskMapper nativeTasks = s.getMapper(LoganTaskMapper.class);
        LoganLogDetailMapper nativeDetails = s.getMapper(LoganLogDetailMapper.class);
        WebLogTaskMapper webTasks = s.getMapper(WebLogTaskMapper.class);
        WebLogDetailMapper webDetails = s.getMapper(WebLogDetailMapper.class);
        assertEquals(1, nativeTasks.selectById(seed.getLongValue("nativeCompleted")).getStatus());
        assertEquals(PAYLOAD, nativeDetails.selectById(seed.getLongValue("nativeCompletedDetail")).getContent());
        assertEquals(PAYLOAD, webDetails.queryById(seed.getLongValue("webCompletedDetail")).getContent());
        assertEquals(analyzed ? 1 : 0, nativeTasks.selectById(seed.getLongValue("nativePending")).getStatus());
        assertEquals(analyzed ? 1 : 0, webTasks.queryByIds(Collections.singletonList(seed.getLongValue("webPending"))).get(0).getStatus());
        if (analyzed) {
            assertEquals(PAYLOAD, nativeDetails.queryByTaskIdTypeKeyword(seed.getLongValue("nativePending"),Collections.singletonList(1),PAYLOAD).get(0).getContent());
            assertEquals(PAYLOAD, webDetails.match(seed.getLongValue("webPending"),0,1440,Collections.singletonList(1)).get(0).getContent());
        }
    }
    private static void verify(SqlSession s) throws Exception {
        Path dir = directory(); JSONObject seed = read(dir.resolve("java-seed.json"));
        verifySeed(s, seed, true);
        // Go must hand over IDs and exact expected payloads. No deriving expectations from DB contents.
        JSONObject go = read(dir.resolve("go-produced.json"));
        long nativeTaskId = go.getLongValue("nativeTask"), nativeDetailId = go.getLongValue("nativeDetail");
        long webTaskId = go.getLongValue("webTask"), webDetailId = go.getLongValue("webDetail");
        assertTrue(nativeTaskId > seed.getLongValue("nativeCompleted"));
        assertTrue(nativeDetailId > seed.getLongValue("nativeCompletedDetail"));
        assertTrue(webTaskId > seed.getLongValue("webCompleted"));
        assertTrue(webDetailId > seed.getLongValue("webCompletedDetail"));
        LoganTaskServiceImpl nativeService = new LoganTaskServiceImpl();
        ReflectionTestUtils.setField(nativeService,"taskMapper",s.getMapper(LoganTaskMapper.class));
        assertEquals(go.getString("nativeDevice"), nativeService.getByTaskId(nativeTaskId).getDeviceId());
        assertEquals(1, nativeService.getByTaskId(nativeTaskId).getStatus());
        LoganLogDetailDTO nativeDetail = s.getMapper(LoganLogDetailMapper.class).selectById(nativeDetailId);
        assertEquals(nativeTaskId,nativeDetail.getTaskId()); assertEquals(go.getString("nativePayload"),nativeDetail.getContent());
        assertEquals(1,s.getMapper(LoganLogDetailMapper.class).queryByTaskIdTypeKeyword(nativeTaskId,Collections.singletonList(nativeDetail.getLogType()),go.getString("nativePayload")).size());
        HandlerDispatcherDefaultImpl dispatcher = new HandlerDispatcherDefaultImpl();
        ReflectionTestUtils.setField(dispatcher,"oneHandler",new LoganTypeOneHandler());
        ReflectionTestUtils.setField(dispatcher,"twoHandler",new LoganTypeTwoHandler());
        ReflectionTestUtils.setField(dispatcher,"threeHandler",new LoganTypeThreeHandler());
        ReflectionTestUtils.setField(dispatcher,"unknownHandler",new LoganTypeUnknownHandler());
        dispatcher.init();
        LoganLogDetailServiceImpl nativeDetailService = new LoganLogDetailServiceImpl();
        ReflectionTestUtils.setField(nativeDetailService,"detailMapper",s.getMapper(LoganLogDetailMapper.class));
        ReflectionTestUtils.setField(nativeDetailService,"taskMapper",s.getMapper(LoganTaskMapper.class));
        ReflectionTestUtils.setField(nativeDetailService,"handlerDispatcher",dispatcher);
        assertEquals(go.getString("nativePayload"),nativeDetailService.getByDetailId(nativeDetailId).getContent());
        assertEquals(nativeDetailId,nativeDetailService.listByDetailIds(Collections.singletonList(nativeDetailId)).get(0).getId());
        assertEquals(nativeDetailId,nativeDetailService.listByTaskIdTypeKeyword(nativeTaskId,Collections.singletonList(nativeDetail.getLogType()),go.getString("nativePayload")).get(0).get(0).getId());

        WebTaskServiceImpl webService = new WebTaskServiceImpl();
        ReflectionTestUtils.setField(webService,"webLogTaskMapper",s.getMapper(WebLogTaskMapper.class));
        WebLogTaskDTO webTask = webService.queryByTaskIds(Collections.singletonList(webTaskId)).get(0);
        assertEquals(go.getString("webDevice"),webTask.getDeviceId()); assertEquals(1,webTask.getStatus());
        WebLogDetailServiceImpl details = new WebLogDetailServiceImpl();
        ReflectionTestUtils.setField(details,"detailMapper",s.getMapper(WebLogDetailMapper.class));
        WebLogDetailDTO webDetail = details.queryById(webDetailId);
        assertEquals(webTaskId,webDetail.getTaskId()); assertEquals(go.getString("webPayload"),webDetail.getContent());
        assertEquals(1,details.match(webTaskId,0,1440,Collections.singletonList(webDetail.getLogType()),go.getString("webPayload")).size());
        assertEquals(webDetailId,details.queryByIds(Collections.singletonList(webDetailId)).get(0).getId());
        JSONObject evidence = new JSONObject(true); evidence.put("status","passed"); evidence.put("javaSeedPreserved",true);
        evidence.put("goAnalyzedJavaPendingNativeAndWeb",true); evidence.put("goGeneratedIDsReadableByJava",true);
        evidence.put("originalJavaServicesAndMappersReadGoPayloads",true); evidence.put("goProduced",go);
        write(dir.resolve("java-recheck.json"),evidence);
        System.out.println("JAVA_ROLLBACK_VERIFIED");
    }
}
