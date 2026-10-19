package com.meituan.logan.baseline;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.meituan.logan.web.controller.*;
import org.junit.Test;
import org.springframework.http.HttpMethod;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;
import org.springframework.test.web.servlet.request.MockHttpServletRequestBuilder;
import org.springframework.test.web.servlet.request.MockMvcRequestBuilders;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;
import java.lang.reflect.Field;
import java.lang.reflect.Proxy;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.*;
import static org.junit.Assert.*;

/** Runs unchanged controllers with Spring MVC/Jackson; service doubles exclude DB/OSS. */
public class ControllerBaselineTest {
 private final ObjectMapper json=new ObjectMapper();
 private Object isolated(Object controller)throws Exception{
  for(Field f:controller.getClass().getDeclaredFields()){
   if(f.getType().isInterface()&&f.getType().getName().startsWith("com.meituan.logan.web.service.")){
    f.setAccessible(true);
    f.set(controller,Proxy.newProxyInstance(f.getType().getClassLoader(),new Class<?>[]{f.getType()},(p,m,a)->{
     Class<?> result=m.getReturnType();
     if(List.class.isAssignableFrom(result))return Collections.emptyList();
     if(result==boolean.class)return false;if(result==long.class)return 0L;if(result==int.class)return 0;return null;
    }));
   }
  }
  return controller;
 }
 @Test public void captureSafeControllerCases()throws Exception{
  Path root=Paths.get("..").toAbsolutePath().normalize();
  byte[] manifestBytes=Files.readAllBytes(root.resolve("contract/cases.json"));
  JsonNode manifest=json.readTree(manifestBytes);
  StringBuilder digest=new StringBuilder();for(byte b:MessageDigest.getInstance("SHA-256").digest(manifestBytes))digest.append(String.format("%02x",b));
  Path output=root.resolve("contract/captures/java-controller");Files.createDirectories(output);Files.deleteIfExists(output.resolve("run.json"));
  org.apache.log4j.Logger.getRootLogger().addAppender(new org.apache.log4j.varia.NullAppender());
  MockMvc mvc=MockMvcBuilders.standaloneSetup(isolated(new LoganController()),isolated(new LoganUploadController()),isolated(new WebLogController()),isolated(new WebLogUploadController())).build();
  List<String> passed=new ArrayList<>();
  for(JsonNode c:manifest.get("cases")){
   if(!"safe".equals(c.path("suite").asText()))continue;
   String id=c.path("id").asText();
   MockHttpServletRequestBuilder request=MockMvcRequestBuilders.request(HttpMethod.valueOf(c.path("method").asText()),c.path("path").asText());
   if(c.has("body"))request.content(c.get("body").asText());
   if(c.has("headers")){Iterator<Map.Entry<String,JsonNode>> fields=c.get("headers").fields();while(fields.hasNext()){Map.Entry<String,JsonNode> e=fields.next();request.header(e.getKey(),e.getValue().asText());}}
   MvcResult result=mvc.perform(request).andReturn();
   assertEquals(id+" status",c.path("expected_status").asInt(),result.getResponse().getStatus());
   byte[] body=result.getResponse().getContentAsByteArray();
   if(c.has("expected_json"))assertEquals(id+" body",c.get("expected_json"),json.readTree(body));
   ObjectNode capture=json.createObjectNode();capture.put("version",1);capture.put("case_id",id);capture.put("captured_at",Instant.now().toString());capture.put("manifest_sha256",digest.toString());capture.put("status",result.getResponse().getStatus());
   ObjectNode headers=capture.putObject("headers");
   for(String key:Arrays.asList("Content-Type","Content-Disposition","Location","Access-Control-Allow-Origin","Access-Control-Allow-Credentials","Access-Control-Max-Age")){String value=result.getResponse().getHeader(key);if(value!=null)headers.put(key,value);}
   capture.put("body_base64",Base64.getEncoder().encodeToString(body));json.writerWithDefaultPrettyPrinter().writeValue(output.resolve(id+".json").toFile(),capture);passed.add(id);
  }
  assertEquals("safe controller cases",14,passed.size());
  ObjectNode run=json.createObjectNode();run.put("version",1);run.put("evidence","java-controller-isolated");run.put("manifest_sha256",digest.toString());run.put("complete",true);run.put("created_at",Instant.now().toString());run.set("cases",json.valueToTree(passed));
  json.writerWithDefaultPrettyPrinter().writeValue(output.resolve("run.json").toFile(),run);
 }
 @Test public void serializeLegacyModels()throws Exception{
  Map<String,Object> models=new LinkedHashMap<>();
  com.meituan.logan.web.model.LoganTaskModel task=new com.meituan.logan.web.model.LoganTaskModel();task.setTaskId(9007199254740993L);models.put("nativeTask",task);
  models.put("nativeDetail",new com.meituan.logan.web.model.LoganLogDetailModel());
  models.put("nativeSimple",new com.meituan.logan.web.model.LoganLogSimpleModel());
  com.meituan.logan.web.dto.WebLogTaskDTO web=new com.meituan.logan.web.dto.WebLogTaskDTO();web.setAddTime(0);web.setUpdateTime(new Date(1234));models.put("webTask",web);
  com.meituan.logan.web.dto.WebLogDetailDTO detail=new com.meituan.logan.web.dto.WebLogDetailDTO();detail.setAddTime(0);models.put("webDetail",detail);
  Path output=Paths.get("../contract/captures/java-controller");Files.createDirectories(output);
  json.writerWithDefaultPrettyPrinter().writeValue(output.resolve("java-models.json").toFile(),models);
 }
}
