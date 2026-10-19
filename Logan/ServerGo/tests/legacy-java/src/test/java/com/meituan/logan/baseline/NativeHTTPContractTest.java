package com.meituan.logan.baseline;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.meituan.logan.web.controller.*;
import com.meituan.logan.web.filter.CORSFilter;
import com.meituan.logan.web.service.*;
import com.meituan.logan.web.enums.ResultEnum;
import com.meituan.logan.web.model.LoganTaskModel;
import org.junit.Test;
import org.springframework.test.web.servlet.*;
import org.springframework.test.web.servlet.request.*;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;
import org.springframework.web.filter.CharacterEncodingFilter;
import java.lang.reflect.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicReference;
import static org.junit.Assert.*;

public class NativeHTTPContractTest {
 private void field(Object obj,String key,Object value)throws Exception{Field f=obj.getClass().getDeclaredField(key);f.setAccessible(true);f.set(obj,value);}
 private Map<String,Object> capture(MockMvc mvc,String id,String method,String path)throws Exception{
  MockHttpServletRequestBuilder request=MockMvcRequestBuilders.request(org.springframework.http.HttpMethod.valueOf(method),path).header("Origin","http://localhost:3000");
  if(method.equals("POST"))request.content(new byte[]{1,2,3});
  if(method.equals("OPTIONS"))request.header("Access-Control-Request-Method","POST");
  MvcResult r=mvc.perform(request).andReturn();Map<String,Object> result=new LinkedHashMap<>();result.put("id",id);result.put("method",method);result.put("path",path);result.put("status",r.getResponse().getStatus());
  Map<String,String> headers=new LinkedHashMap<>();for(String key:Arrays.asList("Content-Type","Vary","Access-Control-Allow-Origin","Access-Control-Allow-Credentials","Access-Control-Allow-Methods","Access-Control-Allow-Headers","Access-Control-Max-Age")){String v=r.getResponse().getHeader(key);if(v!=null)headers.put(key,v);}result.put("headers",headers);result.put("body",Base64.getEncoder().encodeToString(r.getResponse().getContentAsByteArray()));return result;
 }
 @Test public void captureNativeHTTPWithOriginalFilters()throws Exception{
  org.apache.log4j.Logger.getRootLogger().addAppender(new org.apache.log4j.varia.NullAppender());
  AtomicReference<ResultEnum> outcome=new AtomicReference<>(ResultEnum.ERROR_DECRYPT);
  LoganUploadController upload=new LoganUploadController();LoganController download=new LoganController();
  LoganTaskService tasks=(LoganTaskService)Proxy.newProxyInstance(LoganTaskService.class.getClassLoader(),new Class<?>[]{LoganTaskService.class},(p,m,a)->{
   if(m.getName().equals("insertTask"))return 0L;
   if(m.getName().equals("getByTaskId")){if(((Long)a[0])==404L)return null;LoganTaskModel task=new LoganTaskModel();task.setLogFileName("test.log");return task;}
   return null;
  });
  field(upload,"taskService",tasks);field(upload,"fileService",(LoganLogFileService)(stream,name)->outcome.get());field(download,"taskService",tasks);
  MockMvc mvc=MockMvcBuilders.standaloneSetup(upload,download).addFilters(new CharacterEncodingFilter("utf-8"),new CORSFilter()).build();
  // StandaloneMockMvcBuilder omits this adapter; mvc:annotation-driven in the
  // production XML supplies it for Spring's preflight HttpRequestHandler.
  Field adaptersField=org.springframework.web.servlet.DispatcherServlet.class.getDeclaredField("handlerAdapters");adaptersField.setAccessible(true);
  List<org.springframework.web.servlet.HandlerAdapter> adapters=new ArrayList<>((List<org.springframework.web.servlet.HandlerAdapter>)adaptersField.get(mvc.getDispatcherServlet()));adapters.add(new org.springframework.web.servlet.mvc.HttpRequestHandlerAdapter());adaptersField.set(mvc.getDispatcherServlet(),adapters);
  List<Map<String,Object>> rows=new ArrayList<>();
  rows.add(capture(mvc,"upload-decrypt","POST","/logan/upload.json"));outcome.set(ResultEnum.SUCCESS);
  rows.add(capture(mvc,"upload-database","POST","/logan/upload.json"));
  rows.add(capture(mvc,"download-url","GET","/logan/task/42/download.json"));
  rows.add(capture(mvc,"download-url-invalid","GET","/logan/task/0/download.json"));
  rows.add(capture(mvc,"download-url-missing","GET","/logan/task/404/download.json"));
  rows.add(capture(mvc,"download-url-binding","GET","/logan/task/abc/download.json"));
  rows.add(capture(mvc,"download-url-overflow","GET","/logan/task/2147483648/download.json"));
  rows.add(capture(mvc,"download-no-name","GET","/logan/downing"));
  rows.add(capture(mvc,"preflight-upload","OPTIONS","/logan/upload.json"));
  assertEquals(9,rows.size());
  Path out=Paths.get("../contract/captures/java-controller/java-native-http.json");Files.createDirectories(out.getParent());new ObjectMapper().writerWithDefaultPrettyPrinter().writeValue(out.toFile(),rows);
 }
}
