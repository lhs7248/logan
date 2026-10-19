package com.meituan.logan.baseline;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.meituan.logan.web.controller.WebLogController;
import com.meituan.logan.web.dto.WebLogTaskDTO;
import com.meituan.logan.web.mapper.WebLogTaskMapper;
import com.meituan.logan.web.model.WebLogTaskModel;
import com.meituan.logan.web.service.WebTaskService;
import com.meituan.logan.web.service.impl.WebTaskServiceImpl;
import java.lang.reflect.*;
import java.nio.file.*;
import java.util.*;
import org.junit.Test;
import static org.junit.Assert.*;
public class WebBehaviorVectorsTest {
 private void field(Object o,String k,Object v)throws Exception{Field f=o.getClass().getDeclaredField(k);f.setAccessible(true);f.set(o,v);}
 @Test public void originalAggregationAndAnalyzedOverwrite()throws Exception{List<WebLogTaskDTO> rows=new ArrayList<>();for(int i=0;i<5;i++){WebLogTaskDTO t=new WebLogTaskDTO();t.setTaskId(i+1);t.setDeviceId("d");t.setPageNum(1);t.setLogDate(1609430400000L+86400000L*i);t.setAddTime(2000);rows.add(t);}WebTaskService tasks=(WebTaskService)Proxy.newProxyInstance(WebTaskService.class.getClassLoader(),new Class<?>[]{WebTaskService.class},(p,m,a)->new ArrayList<>(rows));WebLogController c=new WebLogController();field(c,"webTaskService",tasks);Object response=c.search(1,2,"d");List<String> calls=new ArrayList<>();WebLogTaskMapper mapper=(WebLogTaskMapper)Proxy.newProxyInstance(WebLogTaskMapper.class.getClassLoader(),new Class<?>[]{WebLogTaskMapper.class},(p,m,a)->{calls.add(m.getName());if(m.getName().equals("exist")){WebLogTaskDTO old=new WebLogTaskDTO();old.setTaskId(7);old.setStatus(1);return old;}return null;});WebTaskServiceImpl save=new WebTaskServiceImpl();field(save,"webLogTaskMapper",mapper);WebLogTaskModel input=new WebLogTaskModel();input.setDeviceId("d");input.setLogPageNo(1);input.setLogDate(1609430400000L);input.setContent("new");assertTrue(save.saveTask(input));assertEquals(Arrays.asList("exist","updateContent"),calls);Map<String,Object> out=new LinkedHashMap<>();out.put("evidence","Original Java controller aggregation and WebTaskServiceImpl mapper calls with isolated substitutes");out.put("search",response);out.put("analyzedOverwriteCalls",calls);Path path=Paths.get("../contract/captures/java-controller/java-web-behavior.json");Files.createDirectories(path.getParent());new ObjectMapper().writerWithDefaultPrettyPrinter().writeValue(path.toFile(),out);}
}
